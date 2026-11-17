package input

import (
	"image"
	"path/filepath"
	"reflect"
	"testing"

	"gioui.org/io/key"

	"github.com/hhhapz/scry/scrcpy/proto"
)

func press(n key.Name, m key.Modifiers) key.Event {
	return key.Event{Name: n, Modifiers: m, State: key.Press}
}
func release(n key.Name, m key.Modifiers) key.Event {
	return key.Event{Name: n, Modifiers: m, State: key.Release}
}

// The rules of scrcpy's keyboard_sdk.c, mode by mode.
func TestKeyModes(t *testing.T) {
	cases := []struct {
		mode    KeyMode
		name    key.Name
		mods    key.Modifiers
		keycode uint32
		key     bool // sent as a key event
	}{
		{Mixed, key.NameReturn, 0, proto.KeycodeEnter, true},
		{Mixed, "A", 0, proto.KeycodeA, true},
		{Mixed, "W", key.ModShift, proto.KeycodeA + 22, true},
		{Mixed, key.NameSpace, 0, proto.KeycodeSpace, true},
		{Mixed, "1", 0, 0, false}, // digits go as text
		{Mixed, ",", 0, 0, false},
		{Text, "A", 0, 0, false},                       // letters as text...
		{Text, "A", key.ModCtrl, proto.KeycodeA, true}, // ...except shortcuts
		{Text, key.NameLeftArrow, 0, proto.KeycodeDpadLeft, true},
		{Raw, "1", 0, proto.Keycode0 + 1, true},
		{Raw, "/", 0, proto.KeycodeSlash, true},
		{Mixed, key.NameF5, 0, proto.KeycodeF1 + 4, true},
		{Mixed, key.NameF12, 0, proto.KeycodeF1 + 11, true},
	}
	for _, c := range cases {
		code, ok := Keycode(c.name, c.mods, c.mode)
		if ok != c.key || (ok && code != c.keycode) {
			t.Errorf("mode %d %q %v: got %d %v, want %d %v", c.mode, c.name, c.mods, code, ok, c.keycode, c.key)
		}
	}
}

func TestTextIsComplementary(t *testing.T) {
	k := &Keyboard{}
	for _, s := range []string{"a", "Z", " "} {
		if _, ok := k.Text(s); ok {
			t.Errorf("mixed mode sent %q as text too (it went as a key)", s)
		}
	}
	for _, s := range []string{"1", "!", "é", "€", "ab"} {
		if m, ok := k.Text(s); !ok || m.Text != s {
			t.Errorf("mixed mode dropped text %q", s)
		}
	}
	if _, ok := (&Keyboard{Mode: Raw}).Text("é"); ok {
		t.Error("raw mode sent text")
	}
	if m, ok := (&Keyboard{Mode: Text}).Text("a"); !ok || m.Text != "a" {
		t.Error("text mode dropped a letter")
	}
}

func TestKeyRepeatAndMeta(t *testing.T) {
	k := &Keyboard{}
	m1, _ := k.Key(press("A", key.ModShift|key.ModCtrl))
	m2, _ := k.Key(press("A", key.ModShift|key.ModCtrl))
	m3, _ := k.Key(release("A", 0))
	if m1.Action != proto.KeyDown || m1.Repeat != 0 || m2.Repeat != 1 || m3.Action != proto.KeyUp {
		t.Fatalf("press/repeat/release: %+v %+v %+v", m1, m2, m3)
	}
	if m1.MetaState&proto.MetaShiftOn == 0 || m1.MetaState&proto.MetaCtrlOn == 0 {
		t.Fatalf("meta %#x lacks shift and ctrl", m1.MetaState)
	}
	k.NoRepeat = true
	k.Key(press("B", 0))
	if _, ok := k.Key(press("B", 0)); ok {
		t.Fatal("repeat sent with NoRepeat")
	}
	if rel := k.Release(); len(rel) != 1 || rel[0].Action != proto.KeyUp || rel[0].Keycode != proto.KeycodeA+1 {
		t.Fatalf("release held keys: %+v", rel)
	}
}

func at(x, y int32) proto.Position { return proto.Position{X: x, Y: y, Width: 1000, Height: 2000} }

func TestMouseBindings(t *testing.T) {
	var m Mouse
	if got := m.Press(at(1, 2), Secondary, false, false); got[0].Type != proto.TypeBackOrScreenOn || got[0].Action != proto.KeyDown {
		t.Fatalf("right click: %+v", got)
	}
	if got := m.Release(at(1, 2), Secondary); got[0].Type != proto.TypeBackOrScreenOn || got[0].Action != proto.KeyUp {
		t.Fatalf("right release: %+v", got)
	}
	if got := m.Press(at(1, 2), Tertiary, false, false); got[0].Keycode != proto.KeycodeHome {
		t.Fatalf("middle click: %+v", got)
	}
	m.Release(at(1, 2), Tertiary)
	if got := m.Press(at(1, 2), Forward, false, false); got[0].Type != proto.TypeExpandNotificationPanel {
		t.Fatalf("5th button: %+v", got)
	}
	m.Release(at(1, 2), Forward)
	// Shift: forwarded as a real mouse button.
	got := m.Press(at(5, 6), Secondary, false, true)
	if got[0].Type != proto.TypeInjectTouch || got[0].PointerID != proto.PointerMouse || got[0].Buttons != uint32(Secondary) {
		t.Fatalf("shift+right: %+v", got)
	}
	m.Release(at(5, 6), Secondary)
}

func TestMouseDragAndHover(t *testing.T) {
	m := Mouse{Hover: true}
	want := []byte{proto.MotionHoverMove, proto.MotionDown, proto.MotionMove, proto.MotionUp, proto.MotionHoverMove}
	var got []byte
	for _, ms := range [][]proto.Control{
		m.Move(at(1, 1)),
		m.Press(at(1, 1), Primary, false, false),
		m.Move(at(50, 60)),
		m.Release(at(50, 60), Primary),
		m.Move(at(70, 80)),
	} {
		for _, c := range ms {
			got = append(got, c.Action)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actions %v, want %v", got, want)
	}
	if out := m.Release(at(0, 0), Primary); out != nil {
		t.Fatal("release without press sent something")
	}
	if out := (&Mouse{}).Move(at(1, 1)); out != nil {
		t.Fatal("hover sent with Hover off")
	}
}

func TestPinch(t *testing.T) {
	var m Mouse
	down := m.Press(at(300, 500), Primary, true, false)
	if len(down) != 2 || down[1].PointerID != proto.PointerVirtualFinger || down[1].Position.X != 700 || down[1].Position.Y != 1500 {
		t.Fatalf("pinch start: %+v", down)
	}
	if mv := m.Move(at(200, 400)); len(mv) != 2 || mv[1].Position.X != 800 {
		t.Fatalf("pinch move: %+v", mv)
	}
	if up := m.Release(at(200, 400), Primary); len(up) != 2 || up[0].Action != proto.MotionUp {
		t.Fatalf("pinch end: %+v", up)
	}
}

func TestScrollDirection(t *testing.T) {
	var m Mouse
	s := m.Scroll(at(1, 1), 0, 2) // wheel down on the desktop
	if s[0].VScroll != -2 {
		t.Fatalf("scroll down sent %v, want -2 (Android: positive is up)", s[0].VScroll)
	}
}

var frame = image.Pt(1000, 2000)

func TestKeymapTap(t *testing.T) {
	k := Keymap{Layout: &Layout{Bindings: []Binding{{Kind: Tap, Key: "Space", X: 0.5, Y: 0.25}}}}
	down, ok := k.Key(press(key.NameSpace, 0), frame)
	if !ok || len(down) != 1 || down[0].Action != proto.MotionDown || down[0].Position.X != 500 || down[0].Position.Y != 500 {
		t.Fatalf("tap down: %+v %v", down, ok)
	}
	if again, ok := k.Key(press(key.NameSpace, 0), frame); !ok || len(again) != 0 {
		t.Fatalf("auto-repeat sent %+v", again)
	}
	up, _ := k.Key(release(key.NameSpace, 0), frame)
	if len(up) != 1 || up[0].Action != proto.MotionUp {
		t.Fatalf("tap up: %+v", up)
	}
	if _, ok := k.Key(press("Q", 0), frame); ok {
		t.Fatal("unbound key handled")
	}
}

func TestKeymapJoystick(t *testing.T) {
	k := Keymap{Layout: &Layout{Bindings: []Binding{{Kind: Joystick, Up: "W", Left: "A", Down: "S", Right: "D", X: 0.2, Y: 0.8, Radius: 0.1}}}}
	// centre (200,1600); throw 0.1*1000 = 100
	m, _ := k.Key(press("W", 0), frame)
	if len(m) != 2 || m[0].Action != proto.MotionDown || m[1].Position != (proto.Position{X: 200, Y: 1500, Width: 1000, Height: 2000}) {
		t.Fatalf("W: %+v", m)
	}
	m, _ = k.Key(press("D", 0), frame) // up-right diagonal, same throw
	if len(m) != 1 || m[0].Position.X != 271 || m[0].Position.Y != 1529 {
		t.Fatalf("W+D: %+v", m)
	}
	k.Key(release("W", 0), frame)
	m, _ = k.Key(release("D", 0), frame)
	if len(m) != 2 || m[0].Position.X != 200 || m[1].Action != proto.MotionUp {
		t.Fatalf("release back to centre then up: %+v", m)
	}
	// Two bindings at once are two fingers.
	k.Layout.Bindings = append(k.Layout.Bindings, Binding{Kind: Tap, Key: "E", X: 0.9, Y: 0.9})
	a, _ := k.Key(press("A", 0), frame)
	e, _ := k.Key(press("E", 0), frame)
	if a[0].PointerID == e[0].PointerID {
		t.Fatal("joystick and tap share a finger")
	}
	if rel := k.Release(frame); len(rel) != 2 {
		t.Fatalf("release all: %+v", rel)
	}
}

func TestLayoutsSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scry", "keymaps.json")
	empty, err := LoadLayouts(path)
	if err != nil || len(empty.Layouts) != 0 {
		t.Fatalf("missing file: %+v %v", empty, err)
	}
	ls := Layouts{Layouts: []Layout{{Name: "Game", Bindings: []Binding{{Kind: Tap, Key: "E", X: 0.1, Y: 0.2}}}}}
	if err := ls.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLayouts(path)
	if err != nil || !reflect.DeepEqual(got, ls) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}
