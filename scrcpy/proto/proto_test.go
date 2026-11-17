package proto

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
)

func enc(t *testing.T, c Control) []byte {
	t.Helper()
	b, err := c.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Golden vectors from scrcpy v5.0.1 app/tests/test_control_msg_serialize.c:
// the exact bytes the official client sends.
func TestControlGolden(t *testing.T) {
	cases := []struct {
		name string
		c    Control
		want []byte
	}{
		{"keycode", InjectKeycode(KeyUp, KeycodeEnter, 5, MetaShiftOn|MetaShiftLeftOn), []byte{
			TypeInjectKeycode, 0x01, 0, 0, 0, 0x42, 0, 0, 0, 0x05, 0, 0, 0, 0x41}},
		{"text", InjectText("hello, world!"), append([]byte{TypeInjectText, 0, 0, 0, 0x0d}, "hello, world!"...)},
		{"touch", InjectTouch(MotionDown, 0x1234567887654321, Position{100, 200, 1080, 1920}, 1, ButtonPrimary, ButtonPrimary), []byte{
			TypeInjectTouch, 0x00,
			0x12, 0x34, 0x56, 0x78, 0x87, 0x65, 0x43, 0x21,
			0, 0, 0, 0x64, 0, 0, 0, 0xc8,
			0x04, 0x38, 0x07, 0x80,
			0xff, 0xff,
			0, 0, 0, 1,
			0, 0, 0, 1}},
		{"scroll", InjectScroll(Position{260, 1026, 1080, 1920}, 16, -16, 1), []byte{
			TypeInjectScroll,
			0, 0, 0x01, 0x04, 0, 0, 0x04, 0x02,
			0x04, 0x38, 0x07, 0x80,
			0x7f, 0xff,
			0x80, 0x00,
			0, 0, 0, 1}},
		{"back", BackOrScreenOn(KeyUp), []byte{TypeBackOrScreenOn, 0x01}},
		{"expand", ExpandNotificationPanel(), []byte{TypeExpandNotificationPanel}},
		{"get clipboard", GetClipboard(2), []byte{TypeGetClipboard, 2}},
		{"set clipboard", SetClipboard(0x0102030405060708, "hello, world!", true), append([]byte{
			TypeSetClipboard, 1, 2, 3, 4, 5, 6, 7, 8, 1, 0, 0, 0, 0x0d}, "hello, world!"...)},
		{"display power", SetDisplayPower(true), []byte{TypeSetDisplayPower, 1}},
		{"start app", StartApp("org.mozilla.firefox"), append([]byte{TypeStartApp, 19}, "org.mozilla.firefox"...)},
		{"resize", ResizeDisplay(1920, 1080), []byte{TypeResizeDisplay, 0x07, 0x80, 0x04, 0x38}},
	}
	for _, c := range cases {
		if got := enc(t, c.c); !bytes.Equal(got, c.want) {
			t.Errorf("%s:\n got % x\nwant % x", c.name, got, c.want)
		}
	}
}

// Every message parses back to itself, as the device reads it.
func TestControlRoundTrip(t *testing.T) {
	msgs := []Control{
		InjectKeycode(KeyDown, KeycodeA, 0, MetaCtrlOn),
		InjectText("héllo"),
		InjectTouch(MotionMove, 7, Position{-3, 4000, 2960, 1848}, 0.5, 0, ButtonPrimary),
		InjectScroll(Position{1, 2, 3, 4}, 0, 1, 0),
		BackOrScreenOn(KeyDown),
		CollapsePanels(), RotateDevice(), ResetVideo(), OpenHardKeyboardSettings(),
		GetClipboard(1),
		SetClipboard(9, "copied", false),
		SetDisplayPower(false),
		StartApp("+?firefox"),
		ResizeDisplay(800, 600),
	}
	var buf bytes.Buffer
	for _, m := range msgs {
		buf.Write(enc(t, m))
	}
	for i, want := range msgs {
		got, err := ReadControl(&buf)
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if got.Type == TypeInjectTouch {
			// pressure goes through 16-bit fixed point
			if d := got.Pressure - want.Pressure; d > 1e-4 || d < -1e-4 {
				t.Errorf("pressure %v, want %v", got.Pressure, want.Pressure)
			}
			got.Pressure = want.Pressure
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("message %d:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

func TestTextTruncatesOnCharacterBoundary(t *testing.T) {
	long := bytes.Repeat([]byte("é"), 200) // 400 bytes
	b := enc(t, InjectText(string(long)))
	n := int(b[1])<<24 | int(b[2])<<16 | int(b[3])<<8 | int(b[4])
	if n > MaxInjectText || n%2 != 0 {
		t.Fatalf("text length %d: over the limit or splits a character", n)
	}
}

func TestStream(t *testing.T) {
	var buf bytes.Buffer
	WriteDeviceName(&buf, "SM-X910")
	WriteCodec(&buf, CodecH265)
	WriteSession(&buf, Session{Width: 2960, Height: 1848})
	WritePacket(&buf, Packet{Data: []byte{0, 0, 0, 1, 0x40}, Config: true})
	WritePacket(&buf, Packet{Data: []byte{1, 2, 3}, PTS: 123456, Key: true})
	WritePacket(&buf, Packet{Data: []byte{4}, PTS: 140000})
	WriteSession(&buf, Session{Width: 1848, Height: 2960, ClientResized: true})

	name, err := ReadDeviceName(&buf)
	if err != nil || name != "SM-X910" {
		t.Fatalf("name %q %v", name, err)
	}
	codec, err := ReadCodec(&buf)
	if err != nil || CodecName(codec) != "h265" {
		t.Fatalf("codec %x %v", codec, err)
	}
	r := NewReader(&buf)
	s, _, err := r.Next()
	if err != nil || s == nil || *s != (Session{Width: 2960, Height: 1848}) {
		t.Fatalf("session %+v %v", s, err)
	}
	_, p, _ := r.Next()
	if !p.Config || p.Key || !bytes.Equal(p.Data, []byte{0, 0, 0, 1, 0x40}) {
		t.Fatalf("config packet %+v", p)
	}
	_, p, _ = r.Next()
	if p.Config || !p.Key || p.PTS != 123456 || !bytes.Equal(p.Data, []byte{1, 2, 3}) {
		t.Fatalf("key packet %+v", p)
	}
	_, p, _ = r.Next()
	if p.Key || p.PTS != 140000 {
		t.Fatalf("packet %+v", p)
	}
	s, _, _ = r.Next()
	if s == nil || !s.ClientResized || s.Width != 1848 {
		t.Fatalf("second session %+v", s)
	}
	if _, _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end: %v", err)
	}
}

func TestDisabledStream(t *testing.T) {
	for code, want := range map[uint32]error{StreamDisabled: ErrStreamDisabled, StreamError: ErrStreamConfig} {
		var buf bytes.Buffer
		WriteCodec(&buf, code)
		if _, err := ReadCodec(&buf); !errors.Is(err, want) {
			t.Errorf("code %d: %v, want %v", code, err, want)
		}
	}
}

func TestBadPacketSize(t *testing.T) {
	r := NewReader(bytes.NewReader(make([]byte, 12))) // size 0
	if _, _, err := r.Next(); err == nil {
		t.Fatal("a zero-size packet must be an error")
	}
}

// Golden vectors from app/tests/test_device_msg_deserialize.c.
func TestDeviceMessages(t *testing.T) {
	in := []byte{DeviceClipboard, 0, 0, 0, 3, 'A', 'B', 'C', DeviceAckClipboard, 1, 2, 3, 4, 5, 6, 7, 8}
	r := bytes.NewReader(in)
	m, err := ReadDeviceMessage(r)
	if err != nil || m.Type != DeviceClipboard || m.Text != "ABC" {
		t.Fatalf("clipboard %+v %v", m, err)
	}
	m, err = ReadDeviceMessage(r)
	if err != nil || m.Sequence != 0x0102030405060708 {
		t.Fatalf("ack %+v %v", m, err)
	}
	var buf bytes.Buffer
	WriteDeviceMessage(&buf, DeviceMessage{Type: DeviceClipboard, Text: "ABC"})
	WriteDeviceMessage(&buf, DeviceMessage{Type: DeviceAckClipboard, Sequence: 0x0102030405060708})
	if !bytes.Equal(buf.Bytes(), in) {
		t.Fatalf("write % x", buf.Bytes())
	}
}
