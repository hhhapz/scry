package adb

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseConnect(t *testing.T) {
	for out, ok := range map[string]bool{
		"connected to 192.168.0.241:43629":                            true,
		"already connected to 192.168.0.241:43629":                    true,
		"failed to connect to '192.168.0.9:5555': Connection refused": false,
		"cannot connect to 192.168.0.9:5555: No route to host (113)":  false,
		"failed to authenticate to 192.168.0.241:43629":               false,
		"": false,
	} {
		if err := ParseConnect(out); (err == nil) != ok {
			t.Errorf("%q: %v", out, err)
		}
	}
}

func TestParsePair(t *testing.T) {
	if err := ParsePair("Successfully paired to 192.168.0.241:37199 [guid=adb-R52W90CNQ5F-qfQPC5]"); err != nil {
		t.Fatal(err)
	}
	if err := ParsePair("Enter pairing code: Failed: Wrong password or connection was dropped."); err == nil || !strings.Contains(err.Error(), "Wrong password") {
		t.Fatalf("wrong code: %v", err)
	}
}

// Output captured from the tablet (see docs/samples).
func TestParseMDNS(t *testing.T) {
	out := "List of discovered mdns services\nadb-R52W90CNQ5F-qfQPC5\t_adb-tls-connect._tcp\t192.168.0.241:43629\nscry-abc\t_adb-tls-pairing._tcp.\t192.168.0.241:37199\n"
	want := []Service{
		{Name: "adb-R52W90CNQ5F-qfQPC5", Type: ServiceConnect, Addr: "192.168.0.241:43629"},
		{Name: "scry-abc", Type: ServicePairing, Addr: "192.168.0.241:37199"},
	}
	if got := ParseMDNS(out); !slices.Equal(got, want) {
		t.Fatalf("%+v", got)
	}
}

func TestParseRoute(t *testing.T) {
	out := "10.0.0.0/8 dev rmnet0 proto kernel scope link src 10.1.2.3\n192.168.0.0/24 dev wlan0 proto kernel scope link src 192.168.0.241\n"
	if ip := ParseRoute(out); ip != "192.168.0.241" {
		t.Fatalf("%q, want the wlan address", ip)
	}
	if ip := ParseRoute("default via 10.0.2.2 dev eth0\n10.0.2.0/24 dev eth0 src 10.0.2.15\n"); ip != "10.0.2.15" {
		t.Fatalf("emulator: %q", ip)
	}
}

func TestParseTrack(t *testing.T) {
	a := "R5CT1234ABC\tdevice usb:1-4 product:p model:Pixel_8 device:d transport_id:1\n"
	b := a + "192.168.0.241:43629\tdevice product:gts9uwifixx model:SM_X910 device:gts9uwifi transport_id:2\n"
	frames := "0000" + hex4(len(a)) + a + hex4(len(b)) + b
	var got [][]Device
	if err := ParseTrack(strings.NewReader(frames), func(ds []Device) { got = append(got, ds) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || len(got[0]) != 0 || len(got[1]) != 1 || len(got[2]) != 2 || got[2][1].Model != "SM_X910" {
		t.Fatalf("%+v", got)
	}
}

func hex4(n int) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[n>>12&15], digits[n>>8&15], digits[n>>4&15], digits[n&15]})
}

func TestQRPayload(t *testing.T) {
	q := NewQRPairing()
	if !strings.HasPrefix(q.Name, "scry-") || len(q.Password) != 10 {
		t.Fatalf("%+v", q)
	}
	if p := q.Payload(); p != "WIFI:T:ADB;S:"+q.Name+";P:"+q.Password+";;" {
		t.Fatal(p)
	}
}

func TestWirelessMDNSSerial(t *testing.T) {
	if !(Device{Serial: "adb-R52W90CNQ5F-qfQPC5._adb-tls-connect._tcp"}).Wireless() {
		t.Fatal("an mDNS serial is wireless")
	}
}

// QR pairing against a fake adb playing the phone: the pairing service shows
// up, pairing succeeds, then the connect service appears on another port.
func TestQRPairingFlow(t *testing.T) {
	dir := t.TempDir()
	q := QRPairing{Name: "scry-test", Password: "123456"}
	script := `#!/bin/sh
dir="` + dir + `"
echo "$@" >> "$dir/calls"
case "$1" in
mdns)
	n=$(cat "$dir/n" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$dir/n"
	echo "List of discovered mdns services"
	[ $n -ge 2 ] && printf 'scry-test\t_adb-tls-pairing._tcp\t10.0.0.7:37001\n'
	[ -f "$dir/paired" ] && printf 'adb-XYZ\t_adb-tls-connect._tcp\t10.0.0.7:41000\n'
	exit 0 ;;
pair)
	[ "$3" = "123456" ] && touch "$dir/paired" && echo "Successfully paired to $2 [guid=adb-XYZ]" ;;
connect) echo "connected to $2" ;;
esac
`
	adb := dir + "/adb"
	if err := writeExec(adb, script); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADB", adb)
	var steps []string
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	addr, err := q.Wait(ctx, func(s string) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	if addr != "10.0.0.7:41000" {
		t.Fatalf("connected to %q", addr)
	}
	calls, _ := os.ReadFile(dir + "/calls")
	if !strings.Contains(string(calls), "pair 10.0.0.7:37001 123456") || !strings.Contains(string(calls), "connect 10.0.0.7:41000") {
		t.Fatalf("adb calls:\n%s", calls)
	}
	if len(steps) != 2 {
		t.Fatalf("steps %q", steps)
	}
}

func writeExec(path, script string) error { return os.WriteFile(path, []byte(script), 0o755) }
