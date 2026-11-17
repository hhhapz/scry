package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

// Control message types (client to device), in protocol order.
const (
	TypeInjectKeycode byte = iota
	TypeInjectText
	TypeInjectTouch
	TypeInjectScroll
	TypeBackOrScreenOn
	TypeExpandNotificationPanel
	TypeExpandSettingsPanel
	TypeCollapsePanels
	TypeGetClipboard
	TypeSetClipboard
	TypeSetDisplayPower
	TypeRotateDevice
	TypeUHIDCreate
	TypeUHIDInput
	TypeUHIDDestroy
	TypeOpenHardKeyboardSettings
	TypeStartApp
	TypeResetVideo
	TypeCameraSetTorch
	TypeCameraZoomIn
	TypeCameraZoomOut
	TypeResizeDisplay
	TypeScanFile
)

// Limits from the client.
const (
	MaxInjectText    = 300
	MaxClipboardText = 1<<18 - 14
	MaxAppName       = 255
)

// Pointer ids with a special meaning; fingers use any other value.
const (
	PointerMouse         uint64 = math.MaxUint64     // -1: a mouse
	PointerGenericFinger uint64 = math.MaxUint64 - 1 // -2: a finger from a mouse click
	PointerVirtualFinger uint64 = math.MaxUint64 - 2 // -3: the second finger of a pinch
)

// Position is a point in the video, with the video size it refers to so
// the device can scale it (and drop it if the size is stale).
type Position struct {
	X, Y          int32
	Width, Height uint16
}

func putPosition(b []byte, p Position) {
	binary.BigEndian.PutUint32(b[0:], uint32(p.X))
	binary.BigEndian.PutUint32(b[4:], uint32(p.Y))
	binary.BigEndian.PutUint16(b[8:], p.Width)
	binary.BigEndian.PutUint16(b[10:], p.Height)
}

func getPosition(b []byte) Position {
	return Position{
		X:      int32(binary.BigEndian.Uint32(b[0:])),
		Y:      int32(binary.BigEndian.Uint32(b[4:])),
		Width:  binary.BigEndian.Uint16(b[8:]),
		Height: binary.BigEndian.Uint16(b[10:]),
	}
}

// u16fp encodes f in [0,1] as 16-bit fixed point (pressure).
func u16fp(f float32) uint16 {
	f = max(0, min(1, f))
	u := uint32(f * 65536)
	if u >= 0xffff {
		u = 0xffff
	}
	return uint16(u)
}

// i16fp encodes f in [-1,1] as signed 16-bit fixed point (scroll).
func i16fp(f float32) int16 {
	f = max(-1, min(1, f))
	i := int32(f * 32768)
	if i >= 0x7fff {
		i = 0x7fff
	}
	return int16(i)
}

// truncUTF8 cuts s to at most n bytes without splitting a character.
func truncUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func putString(b []byte, s string, n int) []byte {
	s = truncUTF8(s, n)
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// Control is one control message. Build them with the constructors below;
// Encode serializes, ReadControl parses (for a fake device).
type Control struct {
	Type byte

	Action    byte   // key, touch, back-or-screen-on: AKEY_/AMOTION_EVENT_ACTION_*
	Keycode   uint32 // AKEYCODE_*
	Repeat    uint32
	MetaState uint32 // AMETA_*

	Text string // inject text, set clipboard, start app, scan file

	PointerID    uint64
	Position     Position
	Pressure     float32
	ActionButton uint32 // AMOTION_EVENT_BUTTON_*
	Buttons      uint32

	HScroll, VScroll float32 // scroll steps, -16..16

	CopyKey  byte   // get clipboard: 0 none, 1 copy, 2 cut
	Sequence uint64 // set clipboard: acknowledged by the device when non-zero
	Paste    bool   // set clipboard: also paste
	On       bool   // display power, torch

	Width, Height uint16 // resize display
}

// Key events.
func InjectKeycode(action byte, keycode, repeat, meta uint32) Control {
	return Control{Type: TypeInjectKeycode, Action: action, Keycode: keycode, Repeat: repeat, MetaState: meta}
}

// InjectText types text (at most MaxInjectText bytes are sent).
func InjectText(text string) Control { return Control{Type: TypeInjectText, Text: text} }

// InjectTouch is a touch or mouse event for one pointer.
func InjectTouch(action byte, pointer uint64, pos Position, pressure float32, actionButton, buttons uint32) Control {
	return Control{Type: TypeInjectTouch, Action: action, PointerID: pointer, Position: pos,
		Pressure: pressure, ActionButton: actionButton, Buttons: buttons}
}

// InjectScroll scrolls at pos by h, v steps (each clamped to ±16).
func InjectScroll(pos Position, h, v float32, buttons uint32) Control {
	return Control{Type: TypeInjectScroll, Position: pos, HScroll: h, VScroll: v, Buttons: buttons}
}

// BackOrScreenOn presses BACK, or turns the screen on if it is off.
func BackOrScreenOn(action byte) Control { return Control{Type: TypeBackOrScreenOn, Action: action} }

// Simple messages without a payload.
func ExpandNotificationPanel() Control  { return Control{Type: TypeExpandNotificationPanel} }
func ExpandSettingsPanel() Control      { return Control{Type: TypeExpandSettingsPanel} }
func CollapsePanels() Control           { return Control{Type: TypeCollapsePanels} }
func RotateDevice() Control             { return Control{Type: TypeRotateDevice} }
func OpenHardKeyboardSettings() Control { return Control{Type: TypeOpenHardKeyboardSettings} }
func ResetVideo() Control               { return Control{Type: TypeResetVideo} }

// GetClipboard asks for the device clipboard (after injecting COPY or CUT
// when copyKey is 1 or 2). The device answers with a Clipboard message.
func GetClipboard(copyKey byte) Control { return Control{Type: TypeGetClipboard, CopyKey: copyKey} }

// SetClipboard sets the device clipboard, and pastes it when paste is set.
func SetClipboard(seq uint64, text string, paste bool) Control {
	return Control{Type: TypeSetClipboard, Sequence: seq, Text: text, Paste: paste}
}

// SetDisplayPower turns the device screen on or off, mirroring continuing.
func SetDisplayPower(on bool) Control { return Control{Type: TypeSetDisplayPower, On: on} }

// StartApp starts an app by package name ('?' prefix: by name, '+': force stop first).
func StartApp(name string) Control { return Control{Type: TypeStartApp, Text: name} }

// ResizeDisplay resizes a virtual display created with --new-display.
func ResizeDisplay(w, h uint16) Control { return Control{Type: TypeResizeDisplay, Width: w, Height: h} }

// Encode appends the serialized message to b.
func (c Control) Encode(b []byte) ([]byte, error) {
	b = append(b, c.Type)
	switch c.Type {
	case TypeInjectKeycode:
		b = append(b, c.Action)
		b = binary.BigEndian.AppendUint32(b, c.Keycode)
		b = binary.BigEndian.AppendUint32(b, c.Repeat)
		b = binary.BigEndian.AppendUint32(b, c.MetaState)
	case TypeInjectText:
		b = putString(b, c.Text, MaxInjectText)
	case TypeInjectTouch:
		b = append(b, c.Action)
		b = binary.BigEndian.AppendUint64(b, c.PointerID)
		var pos [12]byte
		putPosition(pos[:], c.Position)
		b = append(b, pos[:]...)
		b = binary.BigEndian.AppendUint16(b, u16fp(c.Pressure))
		b = binary.BigEndian.AppendUint32(b, c.ActionButton)
		b = binary.BigEndian.AppendUint32(b, c.Buttons)
	case TypeInjectScroll:
		var pos [12]byte
		putPosition(pos[:], c.Position)
		b = append(b, pos[:]...)
		b = binary.BigEndian.AppendUint16(b, uint16(i16fp(c.HScroll/16)))
		b = binary.BigEndian.AppendUint16(b, uint16(i16fp(c.VScroll/16)))
		b = binary.BigEndian.AppendUint32(b, c.Buttons)
	case TypeBackOrScreenOn:
		b = append(b, c.Action)
	case TypeGetClipboard:
		b = append(b, c.CopyKey)
	case TypeSetClipboard:
		b = binary.BigEndian.AppendUint64(b, c.Sequence)
		b = append(b, boolByte(c.Paste))
		b = putString(b, c.Text, MaxClipboardText)
	case TypeSetDisplayPower, TypeCameraSetTorch:
		b = append(b, boolByte(c.On))
	case TypeStartApp:
		s := truncUTF8(c.Text, MaxAppName)
		b = append(b, byte(len(s)))
		b = append(b, s...)
	case TypeResizeDisplay:
		b = binary.BigEndian.AppendUint16(b, c.Width)
		b = binary.BigEndian.AppendUint16(b, c.Height)
	case TypeScanFile:
		b = putString(b, c.Text, 256)
	case TypeExpandNotificationPanel, TypeExpandSettingsPanel, TypeCollapsePanels, TypeRotateDevice,
		TypeOpenHardKeyboardSettings, TypeResetVideo, TypeCameraZoomIn, TypeCameraZoomOut:
	default:
		return nil, fmt.Errorf("scrcpy: cannot encode control type %d", c.Type)
	}
	return b, nil
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

// ReadControl parses one control message, as the device does.
func ReadControl(r io.Reader) (Control, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return Control{}, err
	}
	c := Control{Type: t[0]}
	read := func(n int) ([]byte, error) {
		b := make([]byte, n)
		_, err := io.ReadFull(r, b)
		return b, err
	}
	str := func() (string, error) {
		n, err := read(4)
		if err != nil {
			return "", err
		}
		l := binary.BigEndian.Uint32(n)
		if l > MaxClipboardText {
			return "", errors.New("scrcpy: string too long")
		}
		s, err := read(int(l))
		return string(s), err
	}
	var err error
	var b []byte
	switch c.Type {
	case TypeInjectKeycode:
		if b, err = read(13); err == nil {
			c.Action = b[0]
			c.Keycode = binary.BigEndian.Uint32(b[1:])
			c.Repeat = binary.BigEndian.Uint32(b[5:])
			c.MetaState = binary.BigEndian.Uint32(b[9:])
		}
	case TypeInjectText, TypeScanFile:
		c.Text, err = str()
	case TypeInjectTouch:
		if b, err = read(31); err == nil {
			c.Action = b[0]
			c.PointerID = binary.BigEndian.Uint64(b[1:])
			c.Position = getPosition(b[9:])
			c.Pressure = float32(binary.BigEndian.Uint16(b[21:])) / 65536
			c.ActionButton = binary.BigEndian.Uint32(b[23:])
			c.Buttons = binary.BigEndian.Uint32(b[27:])
		}
	case TypeInjectScroll:
		if b, err = read(20); err == nil {
			c.Position = getPosition(b)
			c.HScroll = float32(int16(binary.BigEndian.Uint16(b[12:]))) / 32768 * 16
			c.VScroll = float32(int16(binary.BigEndian.Uint16(b[14:]))) / 32768 * 16
			c.Buttons = binary.BigEndian.Uint32(b[16:])
		}
	case TypeBackOrScreenOn:
		if b, err = read(1); err == nil {
			c.Action = b[0]
		}
	case TypeGetClipboard:
		if b, err = read(1); err == nil {
			c.CopyKey = b[0]
		}
	case TypeSetClipboard:
		if b, err = read(9); err == nil {
			c.Sequence = binary.BigEndian.Uint64(b)
			c.Paste = b[8] != 0
			c.Text, err = str()
		}
	case TypeSetDisplayPower, TypeCameraSetTorch:
		if b, err = read(1); err == nil {
			c.On = b[0] != 0
		}
	case TypeStartApp:
		if b, err = read(1); err == nil {
			b, err = read(int(b[0]))
			c.Text = string(b)
		}
	case TypeResizeDisplay:
		if b, err = read(4); err == nil {
			c.Width = binary.BigEndian.Uint16(b)
			c.Height = binary.BigEndian.Uint16(b[2:])
		}
	case TypeExpandNotificationPanel, TypeExpandSettingsPanel, TypeCollapsePanels, TypeRotateDevice,
		TypeOpenHardKeyboardSettings, TypeResetVideo, TypeCameraZoomIn, TypeCameraZoomOut:
	default:
		return c, fmt.Errorf("scrcpy: unknown control type %d", c.Type)
	}
	return c, err
}

// Device message types (device to client).
const (
	DeviceClipboard    byte = 0
	DeviceAckClipboard byte = 1
	DeviceUHIDOutput   byte = 2
)

// DeviceMessage is a message from the device.
type DeviceMessage struct {
	Type     byte
	Text     string // Clipboard: the device clipboard changed
	Sequence uint64 // AckClipboard: a SetClipboard with this sequence took effect
	UHIDID   uint16
	Data     []byte
}

// ReadDeviceMessage reads one device message.
func ReadDeviceMessage(r io.Reader) (DeviceMessage, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return DeviceMessage{}, err
	}
	m := DeviceMessage{Type: t[0]}
	switch m.Type {
	case DeviceClipboard:
		var n [4]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return m, err
		}
		l := binary.BigEndian.Uint32(n[:])
		if l > MaxClipboardText+14 {
			return m, errors.New("scrcpy: clipboard too long")
		}
		b := make([]byte, l)
		if _, err := io.ReadFull(r, b); err != nil {
			return m, err
		}
		m.Text = string(b)
	case DeviceAckClipboard:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return m, err
		}
		m.Sequence = binary.BigEndian.Uint64(b[:])
	case DeviceUHIDOutput:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return m, err
		}
		m.UHIDID = binary.BigEndian.Uint16(b[:])
		m.Data = make([]byte, binary.BigEndian.Uint16(b[2:]))
		if _, err := io.ReadFull(r, m.Data); err != nil {
			return m, err
		}
	default:
		return m, fmt.Errorf("scrcpy: unknown device message %d", m.Type)
	}
	return m, nil
}

// WriteDeviceMessage writes a device message (fake devices).
func WriteDeviceMessage(w io.Writer, m DeviceMessage) error {
	b := []byte{m.Type}
	switch m.Type {
	case DeviceClipboard:
		b = binary.BigEndian.AppendUint32(b, uint32(len(m.Text)))
		b = append(b, m.Text...)
	case DeviceAckClipboard:
		b = binary.BigEndian.AppendUint64(b, m.Sequence)
	case DeviceUHIDOutput:
		b = binary.BigEndian.AppendUint16(b, m.UHIDID)
		b = binary.BigEndian.AppendUint16(b, uint16(len(m.Data)))
		b = append(b, m.Data...)
	default:
		return fmt.Errorf("scrcpy: unknown device message %d", m.Type)
	}
	_, err := w.Write(b)
	return err
}
