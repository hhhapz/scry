// Package input turns desktop keyboard and mouse events into scrcpy control
// messages, following scrcpy's own SDK keyboard and mouse behaviour
// (app/src/keyboard_sdk.c, mouse_sdk.c, input_manager.c). It knows Gio's key
// names but nothing about widgets, so it is tested on its own.
package input

import (
	"unicode/utf8"

	"gioui.org/io/key"

	"github.com/hhhapz/scry/scrcpy/proto"
)

// KeyMode decides which keys go as key events and which as text, as
// scrcpy's --prefer-text and --raw-key-events do.
type KeyMode int

const (
	// Mixed (scrcpy's default): letters and space as key events, so games
	// see WASD; digits, punctuation and other characters as text, so they
	// come out right whatever the device's keyboard layout.
	Mixed KeyMode = iota
	// Text: letters and space as text too (better for accents and IMEs),
	// unless Ctrl is held (shortcuts).
	Text
	// Raw: every key as a key event, never text.
	Raw
)

// Keyboard translates key and text events. Not safe for concurrent use.
type Keyboard struct {
	Mode KeyMode
	// NoRepeat drops auto-repeated presses (scrcpy's --no-key-repeat).
	NoRepeat bool

	held map[key.Name]uint32 // pressed keys and their repeat counts
}

// special keys go as key events in every mode.
var special = map[key.Name]uint32{
	key.NameReturn:         proto.KeycodeEnter,
	key.NameEnter:          proto.KeycodeNumpadEnter,
	key.NameEscape:         proto.KeycodeEscape,
	key.NameDeleteBackward: proto.KeycodeDel,
	key.NameTab:            proto.KeycodeTab,
	key.NamePageUp:         proto.KeycodePageUp,
	key.NameDeleteForward:  proto.KeycodeForwardDel,
	key.NameHome:           proto.KeycodeMoveHome,
	key.NameEnd:            proto.KeycodeMoveEnd,
	key.NamePageDown:       proto.KeycodePageDown,
	key.NameRightArrow:     proto.KeycodeDpadRight,
	key.NameLeftArrow:      proto.KeycodeDpadLeft,
	key.NameDownArrow:      proto.KeycodeDpadDown,
	key.NameUpArrow:        proto.KeycodeDpadUp,
	key.NameCtrl:           proto.KeycodeCtrlLeft,
	key.NameShift:          proto.KeycodeShiftLeft,
	key.NameAlt:            proto.KeycodeAltLeft,
	key.NameSuper:          proto.KeycodeMetaLeft,
	key.NameCommand:        proto.KeycodeMetaLeft,
	key.NameBack:           proto.KeycodeBack,
}

// punct are digits and punctuation: key events only in Raw mode.
var punct = map[key.Name]uint32{
	",": proto.KeycodeComma, ".": proto.KeycodePeriod, "-": proto.KeycodeMinus,
	"=": proto.KeycodeEquals, "[": proto.KeycodeLeftBracket, "]": proto.KeycodeRightBracket,
	"\\": proto.KeycodeBackslash, ";": proto.KeycodeSemicolon, "'": proto.KeycodeApostrophe,
	"/": proto.KeycodeSlash, "`": proto.KeycodeGrave,
}

// Keycode is the Android key code for a Gio key name in the given mode,
// with mods held. ok is false when the key goes as text instead.
func Keycode(name key.Name, mods key.Modifiers, mode KeyMode) (uint32, bool) {
	if k, ok := special[name]; ok {
		return k, true
	}
	// F-keys: scrcpy drops them; scry forwards them in every mode.
	if len(name) == 2 && name[0] == 'F' && name[1] >= '1' && name[1] <= '9' {
		return proto.KeycodeF1 + uint32(name[1]-'1'), true
	}
	if name == key.NameF10 || name == key.NameF11 || name == key.NameF12 {
		return proto.KeycodeF1 + 9 + uint32(name[2]-'0'), true
	}
	if mode == Text && mods&key.ModCtrl == 0 {
		return 0, false
	}
	if name == key.NameSpace {
		return proto.KeycodeSpace, true
	}
	if len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z' {
		return proto.KeycodeA + uint32(name[0]-'A'), true
	}
	if mode == Raw {
		if len(name) == 1 && name[0] >= '0' && name[0] <= '9' {
			return proto.Keycode0 + uint32(name[0]-'0'), true
		}
		if k, ok := punct[name]; ok {
			return k, true
		}
	}
	return 0, false
}

// MetaState converts Gio modifiers to Android meta flags (left keys).
func MetaState(m key.Modifiers) uint32 {
	var s uint32
	if m&key.ModShift != 0 {
		s |= proto.MetaShiftOn | proto.MetaShiftLeftOn
	}
	if m&key.ModCtrl != 0 {
		s |= proto.MetaCtrlOn | 0x2000 // AMETA_CTRL_LEFT_ON
	}
	if m&key.ModAlt != 0 {
		s |= proto.MetaAltOn | 0x10 // AMETA_ALT_LEFT_ON
	}
	if m&(key.ModSuper|key.ModCommand) != 0 {
		s |= proto.MetaMetaOn | 0x20000 // AMETA_META_LEFT_ON
	}
	return s
}

// Key translates a key press or release. ok is false when nothing is sent
// (the key goes as text, or a dropped repeat).
func (k *Keyboard) Key(e key.Event) (proto.Control, bool) {
	code, ok := Keycode(e.Name, e.Modifiers, k.Mode)
	if !ok {
		return proto.Control{}, false
	}
	if k.held == nil {
		k.held = make(map[key.Name]uint32)
	}
	action := proto.KeyDown
	var repeat uint32
	if e.State == key.Press {
		if n, down := k.held[e.Name]; down {
			// Gio reports auto-repeat as presses without releases.
			if k.NoRepeat {
				return proto.Control{}, false
			}
			repeat = n + 1
		}
		k.held[e.Name] = repeat
	} else {
		action = proto.KeyUp
		delete(k.held, e.Name)
	}
	return proto.InjectKeycode(action, code, repeat, MetaState(e.Modifiers)), true
}

// Text translates typed text. ok is false when the keys already went as key
// events (letters and space in Mixed mode) or text is never sent (Raw).
func (k *Keyboard) Text(s string) (proto.Control, bool) {
	if s == "" || k.Mode == Raw {
		return proto.Control{}, false
	}
	if k.Mode == Mixed && utf8.RuneCountInString(s) == 1 {
		if c := s[0]; c == ' ' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			return proto.Control{}, false
		}
	}
	return proto.InjectText(s), true
}

// Release lifts every held key: call it when focus leaves the video, or
// the device would see keys stuck down.
func (k *Keyboard) Release() []proto.Control {
	var out []proto.Control
	for name := range k.held {
		if code, ok := Keycode(name, 0, Raw); ok {
			out = append(out, proto.InjectKeycode(proto.KeyUp, code, 0, 0))
		}
	}
	clear(k.held)
	return out
}
