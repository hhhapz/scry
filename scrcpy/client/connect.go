package client

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/hhhapz/scry/scrcpy"
	"github.com/hhhapz/scry/scrcpy/proto"
)

// Config says how to reach the device and what to ask the server for.
type Config struct {
	Serial  string
	Options scrcpy.Options // the session's settings; the server-side ones apply

	ADB        string // adb executable; default $ADB or "adb"
	ServerPath string // scrcpy-server on this computer; default found next to scrcpy
	Version    string // the server's version; default asked from `scrcpy --version`
	// Log receives the server's output lines (its own log), on a goroutine.
	Log func(line string)
}

// devicePath is where the server is pushed, as scrcpy does.
const devicePath = "/data/local/tmp/scrcpy-server.jar"

// Connect starts scrcpy-server on the device and opens a session.
//
// It pushes the installed scrcpy's own server (so client and server
// versions always match), opens an adb reverse tunnel (falling back to a
// forward tunnel when reverse is unavailable), launches the server and
// accepts its sockets.
func Connect(ctx context.Context, cfg Config, h Handler) (*Conn, error) {
	adb := cfg.ADB
	if adb == "" {
		adb = os.Getenv("ADB")
	}
	if adb == "" {
		adb = "adb"
	}
	run := func(args ...string) error {
		out, err := exec.CommandContext(ctx, adb, append([]string{"-s", cfg.Serial}, args...)...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("adb %v: %w: %s", args, err, out)
		}
		return nil
	}
	server := cfg.ServerPath
	if server == "" {
		var err error
		if server, err = FindServer(); err != nil {
			return nil, err
		}
	}
	version := cfg.Version
	if version == "" {
		var err error
		if version, err = InstalledVersion(ctx); err != nil {
			return nil, err
		}
	}
	if err := run("push", server, devicePath); err != nil {
		return nil, err
	}

	scid := newSCID()
	socket := fmt.Sprintf("localabstract:scrcpy_%08x", scid)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	forward := false
	if err := run("reverse", socket, "tcp:"+strconv.Itoa(port)); err != nil {
		// Reverse tunnels need adb support on the device; forward works
		// everywhere but has to poll for the server.
		ln.Close()
		forward = true
		if err := run("forward", "tcp:"+strconv.Itoa(port), socket); err != nil {
			return nil, err
		}
	}
	removeTunnel := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if forward {
			exec.CommandContext(ctx, adb, "-s", cfg.Serial, "forward", "--remove", "tcp:"+strconv.Itoa(port)).Run()
		} else {
			exec.CommandContext(ctx, adb, "-s", cfg.Serial, "reverse", "--remove", socket).Run()
		}
	}

	args := append([]string{"-s", cfg.Serial, "shell", "CLASSPATH=" + devicePath, "app_process", "/",
		"com.genymobile.scrcpy.Server", version}, ServerArgs(cfg.Options, scid, forward)...)
	srv := exec.Command(adb, args...)
	out, _ := srv.StdoutPipe()
	srv.Stderr = srv.Stdout
	if err := srv.Start(); err != nil {
		removeTunnel()
		ln.Close()
		return nil, err
	}
	exited := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if cfg.Log != nil {
				cfg.Log(sc.Text())
			}
		}
		exited <- srv.Wait()
	}()
	stop := func() {
		srv.Process.Kill()
		removeTunnel()
	}

	control := !cfg.Options.ReadOnly
	var video, ctl net.Conn
	if forward {
		video, ctl, err = dialForward(ctx, port, control, exited)
	} else {
		video, ctl, err = acceptReverse(ctx, ln, control, exited)
		ln.Close()
		removeTunnel() // the sockets are open; the tunnel is no longer needed
		removeTunnel = func() {}
	}
	if err != nil {
		stop()
		return nil, err
	}
	var ctlRW io.ReadWriteCloser
	if ctl != nil {
		ctlRW = ctl
	}
	c, err := Open(video, ctlRW, h)
	if err != nil {
		stop()
		return nil, err
	}
	var once sync.Once
	c.cleanup = func() { once.Do(stop) }

	// Requests scrcpy makes over the control channel right after connecting.
	o := cfg.Options
	if control {
		if o.TurnScreenOff {
			c.Send(proto.SetDisplayPower(false))
		}
		if o.StartApp != "" {
			c.Send(proto.StartApp(o.StartApp))
		}
	}
	return c, nil
}

// connectTimeout bounds the wait for the server's sockets.
const connectTimeout = 10 * time.Second

func acceptReverse(ctx context.Context, ln net.Listener, control bool, exited <-chan error) (video, ctl net.Conn, err error) {
	type res struct {
		c   net.Conn
		err error
	}
	accept := func() (net.Conn, error) {
		ch := make(chan res, 1)
		go func() {
			c, err := ln.Accept()
			ch <- res{c, err}
		}()
		select {
		case r := <-ch:
			return r.c, r.err
		case err := <-exited:
			return nil, fmt.Errorf("scrcpy-server exited before connecting: %v", err)
		case <-time.After(connectTimeout):
			return nil, errors.New("scrcpy-server did not connect")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// The server connects video, then control (audio is off: scry plays it
	// through a separate scrcpy).
	if video, err = accept(); err != nil {
		return nil, nil, err
	}
	if control {
		if ctl, err = accept(); err != nil {
			video.Close()
			return nil, nil, err
		}
	}
	return video, ctl, nil
}

func dialForward(ctx context.Context, port int, control bool, exited <-chan error) (video, ctl net.Conn, err error) {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(connectTimeout)
	// adb accepts the TCP connection at once; the server is listening only
	// when its dummy byte arrives. Retry until then.
	for {
		select {
		case err := <-exited:
			return nil, nil, fmt.Errorf("scrcpy-server exited before connecting: %v", err)
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		if video, err = net.Dial("tcp", addr); err == nil {
			var b [1]byte
			video.SetReadDeadline(time.Now().Add(time.Second))
			if _, err = io.ReadFull(video, b[:]); err == nil {
				video.SetReadDeadline(time.Time{})
				break
			}
			video.Close()
		}
		if time.Now().After(deadline) {
			return nil, nil, errors.New("scrcpy-server did not accept a connection")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if control {
		if ctl, err = net.Dial("tcp", addr); err != nil {
			video.Close()
			return nil, nil, err
		}
	}
	return video, ctl, nil
}

func newSCID() uint32 {
	var b [4]byte
	rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:]) & 0x7fffffff // positive, like scrcpy
}

// ServerArgs are the server's parameters for o. Settings that only affect
// the scrcpy window (fullscreen, always on top...) have no server
// equivalent; audio is always off (played by a separate scrcpy).
func ServerArgs(o scrcpy.Options, scid uint32, forward bool) []string {
	a := []string{fmt.Sprintf("scid=%08x", scid), "log_level=info", "audio=false"}
	add := func(cond bool, s string) {
		if cond {
			a = append(a, s)
		}
	}
	switch o.Kind {
	case scrcpy.Camera:
		a = append(a, "video_source=camera")
		add(o.CameraFacing != "", "camera_facing="+o.CameraFacing)
	case scrcpy.AppWindow:
		if o.NewDisplay != "" {
			a = append(a, "new_display="+o.NewDisplay)
		} else {
			a = append(a, "new_display=")
		}
		add(o.Flex, "flex_display=true")
	}
	add(o.MaxSize != 0, "max_size="+strconv.Itoa(o.MaxSize))
	add(o.BitRate != 0, "video_bit_rate="+strconv.Itoa(o.BitRate))
	add(o.MaxFPS != 0, "max_fps="+strconv.Itoa(o.MaxFPS))
	add(o.VideoCodec != "", "video_codec="+o.VideoCodec)
	add(forward, "tunnel_forward=true")
	if o.ReadOnly {
		a = append(a, "control=false")
	} else {
		add(o.ShowTouches, "show_touches=true")
		add(o.StayAwake, "stay_awake=true")
		add(o.PowerOffOnClose, "power_off_on_close=true")
		add(o.NoClipboardSync, "clipboard_autosync=false")
	}
	return a
}

// FindServer locates the installed scrcpy's server: $SCRCPY_SERVER_PATH,
// then the usual install prefixes.
func FindServer() (string, error) {
	if p := os.Getenv("SCRCPY_SERVER_PATH"); p != "" {
		return p, nil
	}
	for _, p := range []string{"/usr/share/scrcpy/scrcpy-server", "/usr/local/share/scrcpy/scrcpy-server",
		"/opt/homebrew/share/scrcpy/scrcpy-server", "/snap/scrcpy/current/usr/share/scrcpy/scrcpy-server"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("scrcpy-server not found: install scrcpy or set SCRCPY_SERVER_PATH")
}

var (
	versionOnce sync.Once
	version     string
	versionErr  error
	versionRe   = regexp.MustCompile(`^scrcpy (\S+)`)
)

// InstalledVersion is the installed scrcpy's version ("5.0.1"). The server
// refuses a client of any other version.
func InstalledVersion(ctx context.Context) (string, error) {
	versionOnce.Do(func() {
		out, err := exec.CommandContext(ctx, "scrcpy", "--version").Output()
		if err != nil {
			versionErr = fmt.Errorf("scrcpy --version: %w", err)
			return
		}
		version, versionErr = ParseVersion(string(out))
	})
	return version, versionErr
}

// ParseVersion reads the version from `scrcpy --version` output.
func ParseVersion(out string) (string, error) {
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("unrecognised scrcpy version output: %q", out)
	}
	return m[1], nil
}
