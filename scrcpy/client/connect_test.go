package client_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hhhapz/scry/scrcpy"
	"github.com/hhhapz/scry/scrcpy/client"
	"github.com/hhhapz/scry/scrcpy/fake"
	"github.com/hhhapz/scry/scrcpy/proto"
)

// The fake adb below runs this test binary as "the server on the device".
func TestMain(m *testing.M) {
	if os.Getenv("SCRY_FAKE_SERVER") != "" {
		os.Exit(fakeServer())
	}
	os.Exit(m.Run())
}

// fakeServer plays scrcpy-server: in reverse mode it connects to the port
// the fake adb recorded for the reverse tunnel; in forward mode it listens
// on the forwarded port and sends the dummy byte, as the real one does.
func fakeServer() int {
	state := os.Getenv("SCRY_FAKE_STATE")
	args := strings.Join(os.Args[1:], " ")
	port, err := os.ReadFile(filepath.Join(state, "port"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "no tunnel:", err)
		return 1
	}
	addr := "127.0.0.1:" + strings.TrimSpace(string(port))
	fmt.Println("[server] INFO: Device: [fake] fake Fake (Android 16)")
	var video, control net.Conn
	if strings.Contains(args, "tunnel_forward=true") {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		video, _ = ln.Accept()
		video.Write([]byte{0}) // the dummy byte
		control, _ = ln.Accept()
	} else {
		video, err = net.Dial("tcp", addr)
		if err == nil {
			control, err = net.Dial("tcp", addr)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	os.WriteFile(filepath.Join(state, "server-args"), []byte(args), 0o644)
	d := &fake.Device{Name: "Fake Phone", Loop: true, Realtime: true}
	d.Serve(video, control)
	return 0
}

// fakeADB writes an adb stand-in that logs each call, keeps the tunnel's
// port, and launches the fake server for "shell ... app_process".
func fakeADB(t *testing.T, reverseWorks bool) (adb, state string) {
	state = t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rev := ":" // succeed, then record the port below
	if !reverseWorks {
		rev = `echo "error: more than one device" >&2; exit 1`
	}
	script := fmt.Sprintf(`#!/bin/sh
state=%q
echo "$@" >> "$state/calls"
shift 2 # -s SERIAL
case "$1" in
push) exit 0 ;;
reverse)
	[ "$2" = "--remove" ] && exit 0
	%s
	echo "${3#tcp:}" > "$state/port" ;;
forward)
	[ "$2" = "--remove" ] && exit 0
	echo "${2#tcp:}" > "$state/port" ;;
shell)
	shift 4 # CLASSPATH=... app_process / com.genymobile.scrcpy.Server
	SCRY_FAKE_SERVER=1 SCRY_FAKE_STATE="$state" exec %q "$@" ;;
esac
`, state, rev, self)
	adb = filepath.Join(state, "adb")
	if err := os.WriteFile(adb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return adb, state
}

func connect(t *testing.T, reverseWorks bool, o scrcpy.Options) (*client.Conn, string, chan proto.Session) {
	adb, state := fakeADB(t, reverseWorks)
	sessions := make(chan proto.Session, 4)
	var logs []string
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := client.Connect(ctx, client.Config{
		Serial: "FAKE123", Options: o, ADB: adb,
		ServerPath: adb, // any file: the fake push ignores it
		Version:    "5.0.1",
		Log:        func(l string) { logs = append(logs, l) },
	}, client.Handler{Session: func(s proto.Session) { sessions <- s }})
	if err != nil {
		calls, _ := os.ReadFile(filepath.Join(state, "calls"))
		t.Fatalf("connect: %v\nadb calls:\n%s\nserver log:\n%s", err, calls, strings.Join(logs, "\n"))
	}
	t.Cleanup(func() { c.Close() })
	return c, state, sessions
}

func calls(t *testing.T, state string) string {
	b, _ := os.ReadFile(filepath.Join(state, "calls"))
	return string(b)
}

func TestConnectReverse(t *testing.T) {
	c, state, sessions := connect(t, true, scrcpy.Options{MaxSize: 1024, StayAwake: true, TurnScreenOff: true})
	if c.DeviceName != "Fake Phone" {
		t.Fatalf("device name %q", c.DeviceName)
	}
	select {
	case s := <-sessions:
		if s.Width != 360 || s.Height != 640 {
			t.Fatalf("session %+v", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no video session")
	}
	log := calls(t, state)
	for _, want := range []string{
		"-s FAKE123 push ", "/data/local/tmp/scrcpy-server.jar",
		"-s FAKE123 reverse localabstract:scrcpy_",
		"-s FAKE123 reverse --remove localabstract:scrcpy_", // removed once connected
		"shell CLASSPATH=/data/local/tmp/scrcpy-server.jar app_process / com.genymobile.scrcpy.Server 5.0.1 scid=",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("adb calls lack %q:\n%s", want, log)
		}
	}
	args, _ := os.ReadFile(filepath.Join(state, "server-args"))
	for _, want := range []string{"5.0.1", "max_size=1024", "stay_awake=true", "audio=false"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("server args %q lack %q", args, want)
		}
	}
	if strings.Contains(string(args), "tunnel_forward") {
		t.Errorf("reverse mode asked for a forward tunnel: %s", args)
	}
	// The scid in the tunnel name and the server argument agree.
	i := strings.Index(log, "scrcpy_")
	scid := log[i+7 : i+15]
	if _, err := strconv.ParseUint(scid, 16, 32); err != nil || !strings.Contains(string(args), "scid="+scid) {
		t.Errorf("tunnel scid %q vs server args %q", scid, args)
	}
}

func TestConnectForwardFallback(t *testing.T) {
	c, state, sessions := connect(t, false, scrcpy.Options{})
	select {
	case <-sessions:
	case <-time.After(10 * time.Second):
		t.Fatal("no video session over the forward tunnel")
	}
	log := calls(t, state)
	if !strings.Contains(log, "forward tcp:") {
		t.Fatalf("no forward tunnel after reverse failed:\n%s", log)
	}
	args, _ := os.ReadFile(filepath.Join(state, "server-args"))
	if !strings.Contains(string(args), "tunnel_forward=true") {
		t.Fatalf("server not told about the forward tunnel: %s", args)
	}
	c.Close()
	// The forward tunnel is removed when the session ends.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(calls(t, state), "forward --remove") {
		if time.Now().After(deadline) {
			t.Fatalf("forward tunnel not removed:\n%s", calls(t, state))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConnectServerDies(t *testing.T) {
	adb, _ := fakeADB(t, true)
	// A server that exits at once (wrong version, crash): Connect must fail
	// fast with the reason, not wait for the timeout.
	bad := filepath.Join(filepath.Dir(adb), "adb-bad")
	os.WriteFile(bad, []byte("#!/bin/sh\ncase \"$3\" in shell) echo 'ERROR: server version mismatch'; exit 1;; esac\nexit 0\n"), 0o755)
	start := time.Now()
	_, err := client.Connect(context.Background(), client.Config{Serial: "X", ADB: bad, ServerPath: bad, Version: "5.0.1"}, client.Handler{})
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v to notice the server died", time.Since(start))
	}
}
