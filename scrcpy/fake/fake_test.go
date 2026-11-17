package fake_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hhhapz/giokit/media"

	"github.com/hhhapz/scry/scrcpy"
	"github.com/hhhapz/scry/scrcpy/client"
	"github.com/hhhapz/scry/scrcpy/fake"
	"github.com/hhhapz/scry/scrcpy/proto"
)

// recorder collects a session's events.
type recorder struct {
	mu       sync.Mutex
	codec    string
	sessions []proto.Session
	packets  []proto.Packet
	device   []proto.DeviceMessage
	closed   chan error
}

func newRecorder() *recorder { return &recorder{closed: make(chan error, 1)} }

func (r *recorder) handler() client.Handler {
	return client.Handler{
		Codec:   func(n string) { r.mu.Lock(); r.codec = n; r.mu.Unlock() },
		Session: func(s proto.Session) { r.mu.Lock(); r.sessions = append(r.sessions, s); r.mu.Unlock() },
		Packet: func(p proto.Packet) {
			r.mu.Lock()
			p.Data = append([]byte(nil), p.Data...)
			r.packets = append(r.packets, p)
			r.mu.Unlock()
		},
		Device: func(m proto.DeviceMessage) { r.mu.Lock(); r.device = append(r.device, m); r.mu.Unlock() },
		Closed: func(err error) { r.closed <- err },
	}
}

func (r *recorder) waitClosed(t *testing.T) error {
	select {
	case err := <-r.closed:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("session did not end")
		return nil
	}
}

// The whole stream arrives intact and decodes: what scry draws.
func TestStreamDecodes(t *testing.T) {
	d := &fake.Device{Name: "Pixel Test"}
	r := newRecorder()
	c, err := d.Connect(r.handler())
	if err != nil {
		t.Fatal(err)
	}
	if c.DeviceName != "Pixel Test" {
		t.Fatalf("device name %q", c.DeviceName)
	}
	if err := r.waitClosed(t); err == nil || !strings.Contains(err.Error(), "closed the stream") {
		t.Fatalf("end of a non-looping file: %v", err)
	}
	if r.codec != "h264" || len(r.sessions) != 1 || r.sessions[0] != (proto.Session{Width: 360, Height: 640}) {
		t.Fatalf("codec %q sessions %+v", r.codec, r.sessions)
	}
	if len(r.packets) != 91 || !r.packets[0].Config || !r.packets[1].Key {
		t.Fatalf("%d packets (want config + 90), first %+v", len(r.packets), r.packets[0])
	}
	dec, err := media.NewDecoder(media.H264, media.Options{Hardware: media.HardwareOff})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	frames := 0
	for _, p := range r.packets {
		err := dec.Decode(media.Packet{Data: p.Data, PTS: p.PTS, Key: p.Key, Config: p.Config}, func(f media.Frame) {
			if f.Width != 360 || f.Height != 640 {
				t.Fatalf("frame %dx%d", f.Width, f.Height)
			}
			frames++
			f.Release()
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if frames != 90 {
		t.Fatalf("%d frames decoded, want 90", frames)
	}
}

// Input reaches the device byte-exact; clipboard requests are answered.
func TestControlReachesDevice(t *testing.T) {
	d := &fake.Device{Loop: true, Realtime: true}
	r := newRecorder()
	c, err := d.Connect(r.handler())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sent := []proto.Control{
		proto.InjectTouch(proto.MotionDown, proto.PointerMouse, proto.Position{X: 10, Y: 20, Width: 360, Height: 640}, 1, proto.ButtonPrimary, proto.ButtonPrimary),
		proto.InjectKeycode(proto.KeyDown, proto.KeycodeHome, 0, 0),
		proto.InjectText("hi"),
		proto.SetClipboard(42, "from the desktop", false),
		proto.GetClipboard(0),
	}
	for _, m := range sent {
		if err := c.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	got := d.WaitControls(len(sent), 5*time.Second)
	if len(got) != len(sent) {
		t.Fatalf("device got %d messages, want %d", len(got), len(sent))
	}
	for i := range sent {
		if got[i].Type != sent[i].Type || got[i].Text != sent[i].Text || got[i].Position != sent[i].Position {
			t.Errorf("message %d: got %+v want %+v", i, got[i], sent[i])
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		n := len(r.device)
		r.mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.device) < 2 || r.device[0].Sequence != 42 || r.device[1].Text != "from the desktop" {
		t.Fatalf("device messages %+v, want ack 42 then the clipboard", r.device)
	}
}

func TestCloseIsClean(t *testing.T) {
	d := &fake.Device{Loop: true, Realtime: true}
	r := newRecorder()
	c, err := d.Connect(r.handler())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	c.Close()
	if err := r.waitClosed(t); err != nil {
		t.Fatalf("Close reported %v, want nil", err)
	}
	if c.Err() != nil {
		t.Fatal(c.Err())
	}
	if err := c.Send(proto.RotateDevice()); err == nil {
		t.Fatal("Send after Close succeeded")
	}
}

// A looped file keeps PTS increasing and resends the codec config.
func TestLoopKeepsTime(t *testing.T) {
	d := &fake.Device{Loop: true}
	r := newRecorder()
	c, err := d.Connect(r.handler())
	if err != nil {
		t.Fatal(err)
	}
	for {
		r.mu.Lock()
		n := len(r.packets)
		r.mu.Unlock()
		if n >= 200 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.Close()
	r.waitClosed(t)
	r.mu.Lock()
	defer r.mu.Unlock()
	last, configs := int64(-1), 0
	for _, p := range r.packets {
		if p.Config {
			configs++
			continue
		}
		if p.PTS <= last {
			t.Fatalf("pts went from %d to %d across a loop", last, p.PTS)
		}
		last = p.PTS
	}
	if configs < 2 {
		t.Fatalf("%d config packets, want one per loop", configs)
	}
}

func TestServerArgs(t *testing.T) {
	got := strings.Join(client.ServerArgs(scrcpy.Options{MaxSize: 1920, BitRate: 16_000_000, MaxFPS: 60, StayAwake: true, VideoCodec: "h265"}, 0xabc, false), " ")
	want := "scid=00000abc log_level=info audio=false max_size=1920 video_bit_rate=16000000 max_fps=60 video_codec=h265 stay_awake=true"
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	ro := strings.Join(client.ServerArgs(scrcpy.Options{ReadOnly: true, StayAwake: true}, 1, true), " ")
	if !strings.Contains(ro, "control=false") || strings.Contains(ro, "stay_awake") || !strings.Contains(ro, "tunnel_forward=true") {
		t.Errorf("read-only args %s", ro)
	}
	vd := strings.Join(client.ServerArgs(scrcpy.Options{Kind: scrcpy.AppWindow}, 1, false), " ")
	if !strings.Contains(vd, "new_display= ") && !strings.HasSuffix(vd, "new_display=") {
		t.Errorf("app window args %s", vd)
	}
}

func TestParseVersion(t *testing.T) {
	v, err := client.ParseVersion("scrcpy 5.0.1 <https://github.com/Genymobile/scrcpy>\n\nDependencies...")
	if err != nil || v != "5.0.1" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := client.ParseVersion("something else"); err == nil {
		t.Fatal("garbage parsed")
	}
}
