package adb

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Executable is the adb to run: $ADB, else "adb".
func Executable() string {
	if a := os.Getenv("ADB"); a != "" {
		return a
	}
	return "adb"
}

func run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, Executable(), args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Connect connects to a device listening for adb over TCP/IP, at host:port
// (port 5555 when omitted). adb exits 0 even when it fails, so the output
// decides.
func Connect(ctx context.Context, addr string) (string, error) {
	if !strings.Contains(addr, ":") {
		addr += ":5555"
	}
	out, err := run(ctx, "connect", addr)
	if e := ParseConnect(out); e != nil {
		return out, e
	}
	if err != nil {
		return out, fmt.Errorf("adb connect: %w: %s", err, out)
	}
	return out, nil
}

// ParseConnect reads `adb connect` output: nil for "connected to" and
// "already connected to", else the reason.
func ParseConnect(out string) error {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "already connected to"), strings.HasPrefix(l, "connected to"):
		return nil
	case out == "":
		return errors.New("adb connect: no answer")
	}
	return errors.New(strings.TrimPrefix(out, "* daemon started successfully\n"))
}

// Disconnect drops a TCP/IP device.
func Disconnect(ctx context.Context, serial string) error {
	out, err := run(ctx, "disconnect", serial)
	if err != nil || strings.Contains(strings.ToLower(out), "error") {
		return fmt.Errorf("adb disconnect: %s", out)
	}
	return nil
}

// Pair pairs with a device in Wireless debugging's "Pair device with
// pairing code" screen (Android 11+).
func Pair(ctx context.Context, addr, code string) error {
	out, err := run(ctx, "pair", addr, code)
	if e := ParsePair(out); e != nil {
		return e
	}
	if err != nil {
		return fmt.Errorf("adb pair: %w: %s", err, out)
	}
	return nil
}

// ParsePair reads `adb pair` output: nil for "Successfully paired".
func ParsePair(out string) error {
	if strings.Contains(out, "Successfully paired") {
		return nil
	}
	if out == "" {
		return errors.New("adb pair: no answer")
	}
	return errors.New(lastLine(out))
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// mDNS service types adb browses.
const (
	ServiceConnect = "_adb-tls-connect._tcp" // a paired device's wireless debugging
	ServicePairing = "_adb-tls-pairing._tcp" // a device showing a pairing screen
	ServiceLegacy  = "_adb._tcp"             // `adb tcpip` devices that advertise
)

// Service is one mDNS advertisement adb found.
type Service struct {
	Name string // instance, e.g. "adb-R52W90CNQ5F-qfQPC5"
	Type string // ServiceConnect, ServicePairing...
	Addr string // host:port
}

// MDNSServices lists what adb's mDNS discovery sees.
func MDNSServices(ctx context.Context) ([]Service, error) {
	out, err := run(ctx, "mdns", "services")
	if err != nil {
		return nil, fmt.Errorf("adb mdns services: %w: %s", err, out)
	}
	return ParseMDNS(out), nil
}

// ParseMDNS parses `adb mdns services`.
func ParseMDNS(out string) []Service {
	var ss []Service
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || !strings.HasPrefix(f[1], "_adb") {
			continue
		}
		ss = append(ss, Service{Name: f[0], Type: strings.TrimSuffix(f[1], "."), Addr: f[2]})
	}
	return ss
}

// TCPIP restarts a USB device's adbd listening on port, for Wi-Fi use.
func TCPIP(ctx context.Context, serial string, port int) error {
	out, err := run(ctx, "-s", serial, "tcpip", strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("adb tcpip: %w: %s", err, out)
	}
	return nil
}

// DeviceIP is the device's Wi-Fi address.
func DeviceIP(ctx context.Context, serial string) (string, error) {
	out, err := run(ctx, "-s", serial, "shell", "ip", "route")
	if err != nil {
		return "", fmt.Errorf("adb shell ip route: %w: %s", err, out)
	}
	if ip := ParseRoute(out); ip != "" {
		return ip, nil
	}
	return "", errors.New("the device has no Wi-Fi address (is Wi-Fi on?)")
}

// ParseRoute finds the source address of the wlan route in `ip route`
// output, or of any route if none is on wlan.
func ParseRoute(out string) string {
	var any string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "src" {
				if strings.Contains(line, "wlan") {
					return f[i+1]
				}
				if any == "" {
					any = f[i+1]
				}
			}
		}
	}
	return any
}

// Track calls onChange with the device list whenever it changes, until ctx
// ends or adb exits. It runs `adb track-devices -l`, which streams the list.
func Track(ctx context.Context, onChange func([]Device)) error {
	cmd := exec.CommandContext(ctx, Executable(), "track-devices", "-l")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	err = ParseTrack(out, onChange)
	cmd.Wait()
	return err
}

// ParseTrack reads track-devices frames: four hex digits of length, then
// that many bytes in the format of `adb devices -l` without its header.
func ParseTrack(r io.Reader, onChange func([]Device)) error {
	br := bufio.NewReader(r)
	var n [4]byte
	for {
		if _, err := io.ReadFull(br, n[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		size, err := strconv.ParseUint(string(n[:]), 16, 32)
		if err != nil {
			return fmt.Errorf("adb track-devices: bad frame length %q", n[:])
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(br, body); err != nil {
			return err
		}
		onChange(ParseDevices(string(body)))
	}
}

// QRPairing is the QR code Android's Wireless debugging scans ("Pair device
// with QR code"): the phone then advertises Name as a pairing service, and
// the computer pairs with Password.
type QRPairing struct {
	Name     string // service instance the phone will advertise
	Password string
}

// NewQRPairing makes random credentials, like Android Studio's.
func NewQRPairing() QRPairing {
	return QRPairing{Name: "scry-" + randString(8, "abcdefghijklmnopqrstuvwxyz0123456789"), Password: randString(10, "0123456789")}
}

// Payload is the text to encode in the QR code.
func (q QRPairing) Payload() string { return "WIFI:T:ADB;S:" + q.Name + ";P:" + q.Password + ";;" }

// Wait watches mDNS until the phone advertises the pairing service, pairs,
// then waits for its connect service and connects. It returns the address
// connected to.
func (q QRPairing) Wait(ctx context.Context, step func(string)) (string, error) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	paired := ""
	for {
		ss, err := MDNSServices(ctx)
		if err == nil {
			for _, s := range ss {
				if paired == "" && s.Type == ServicePairing && s.Name == q.Name {
					step("Pairing with " + s.Addr + "…")
					if err := Pair(ctx, s.Addr, q.Password); err != nil {
						return "", err
					}
					paired = strings.SplitN(s.Addr, ":", 2)[0]
					step("Paired. Connecting…")
				}
				if paired != "" && s.Type == ServiceConnect && strings.HasPrefix(s.Addr, paired+":") {
					if _, err := Connect(ctx, s.Addr); err != nil {
						return "", err
					}
					return s.Addr, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
		}
	}
}

func randString(n int, alphabet string) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}
