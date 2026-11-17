package input

import (
	"encoding/json"
	"image"
	"math"
	"os"
	"path/filepath"

	"gioui.org/io/key"

	"github.com/hhhapz/scry/scrcpy/proto"
)

// BindingKind is what a binding does.
type BindingKind string

const (
	// Tap holds a finger on a spot while the key is down.
	Tap BindingKind = "tap"
	// Joystick drags a finger from its centre toward the direction of the
	// held keys (up, left, down, right), like a virtual thumbstick.
	Joystick BindingKind = "joystick"
)

// Binding maps keys to a touch. Positions are fractions of the frame
// (0..1), so a layout survives resolution and orientation changes.
type Binding struct {
	Kind BindingKind `json:"kind"`
	X    float32     `json:"x"`
	Y    float32     `json:"y"`

	Key string `json:"key,omitempty"` // Tap: a Gio key name ("Space", "E", "⏎")

	// Joystick: the four direction keys and the throw, as a fraction of the
	// frame's shorter side.
	Up     string  `json:"up,omitempty"`
	Left   string  `json:"left,omitempty"`
	Down   string  `json:"down,omitempty"`
	Right  string  `json:"right,omitempty"`
	Radius float32 `json:"radius,omitempty"`
}

// Keys are the keys the binding uses.
func (b Binding) Keys() []key.Name {
	if b.Kind == Joystick {
		var ks []key.Name
		for _, k := range []string{b.Up, b.Left, b.Down, b.Right} {
			if k != "" {
				ks = append(ks, key.Name(k))
			}
		}
		return ks
	}
	if b.Key == "" {
		return nil
	}
	return []key.Name{key.Name(b.Key)}
}

// Layout is a named set of bindings, usually one per game.
type Layout struct {
	Name     string    `json:"name"`
	Bindings []Binding `json:"bindings"`
}

// Keys are every key the layout uses.
func (l *Layout) Keys() []key.Name {
	var ks []key.Name
	for _, b := range l.Bindings {
		ks = append(ks, b.Keys()...)
	}
	return ks
}

// fingerBase starts the pointer ids of mapped touches, clear of the mouse
// and the special ids at the top of the range.
const fingerBase = 0x4b4d_0000 // "KM"

// Keymap plays a layout: key events in, touches out. Not safe for
// concurrent use.
type Keymap struct {
	Layout *Layout

	down map[key.Name]bool
	live map[int]bool // bindings with a finger down
}

// Handles reports whether the layout binds name.
func (k *Keymap) Handles(name key.Name) bool {
	if k.Layout == nil {
		return false
	}
	for _, b := range k.Layout.Bindings {
		for _, n := range b.Keys() {
			if n == name {
				return true
			}
		}
	}
	return false
}

// Key applies a key event for a frame of the given size. handled is false
// for keys the layout does not bind.
func (k *Keymap) Key(e key.Event, frame image.Point) (msgs []proto.Control, handled bool) {
	if k.Layout == nil || frame.X <= 0 || frame.Y <= 0 {
		return nil, false
	}
	if k.down == nil {
		k.down, k.live = make(map[key.Name]bool), make(map[int]bool)
	}
	press := e.State == key.Press
	if press && k.down[e.Name] {
		return nil, k.Handles(e.Name) // auto-repeat: the finger is already down
	}
	for i, b := range k.Layout.Bindings {
		uses := false
		for _, n := range b.Keys() {
			uses = uses || n == e.Name
		}
		if !uses {
			continue
		}
		handled = true
		if press {
			k.down[e.Name] = true
		} else {
			delete(k.down, e.Name)
		}
		msgs = append(msgs, k.update(i, b, frame)...)
	}
	if !handled {
		return nil, false
	}
	return msgs, true
}

// update brings binding i's finger in line with the keys held.
func (k *Keymap) update(i int, b Binding, frame image.Point) []proto.Control {
	id := uint64(fingerBase + i)
	centre := pos(b.X, b.Y, frame)
	switch b.Kind {
	case Tap:
		held := k.down[key.Name(b.Key)]
		switch {
		case held && !k.live[i]:
			k.live[i] = true
			return []proto.Control{proto.InjectTouch(proto.MotionDown, id, centre, 1, 0, 0)}
		case !held && k.live[i]:
			delete(k.live, i)
			return []proto.Control{proto.InjectTouch(proto.MotionUp, id, centre, 0, 0, 0)}
		}
	case Joystick:
		var dx, dy float64
		if k.down[key.Name(b.Up)] {
			dy--
		}
		if k.down[key.Name(b.Down)] {
			dy++
		}
		if k.down[key.Name(b.Left)] {
			dx--
		}
		if k.down[key.Name(b.Right)] {
			dx++
		}
		active := k.down[key.Name(b.Up)] || k.down[key.Name(b.Down)] || k.down[key.Name(b.Left)] || k.down[key.Name(b.Right)]
		if !active {
			if k.live[i] {
				delete(k.live, i)
				return []proto.Control{
					proto.InjectTouch(proto.MotionMove, id, centre, 1, 0, 0),
					proto.InjectTouch(proto.MotionUp, id, centre, 0, 0, 0),
				}
			}
			return nil
		}
		r := float64(b.Radius)
		if r <= 0 {
			r = 0.1
		}
		throw := r * float64(min(frame.X, frame.Y))
		if l := math.Hypot(dx, dy); l > 0 {
			dx, dy = dx/l*throw, dy/l*throw // diagonals as far as straight
		}
		to := centre
		to.X += int32(math.Round(dx))
		to.Y += int32(math.Round(dy))
		var out []proto.Control
		if !k.live[i] {
			k.live[i] = true
			out = append(out, proto.InjectTouch(proto.MotionDown, id, centre, 1, 0, 0))
		}
		return append(out, proto.InjectTouch(proto.MotionMove, id, to, 1, 0, 0))
	}
	return nil
}

// Release lifts every finger: on focus loss, or when the layout changes.
func (k *Keymap) Release(frame image.Point) []proto.Control {
	var out []proto.Control
	if k.Layout != nil {
		for i, b := range k.Layout.Bindings {
			if k.live[i] {
				out = append(out, proto.InjectTouch(proto.MotionUp, uint64(fingerBase+i), pos(b.X, b.Y, frame), 0, 0, 0))
			}
		}
	}
	clear(k.down)
	clear(k.live)
	return out
}

func pos(x, y float32, frame image.Point) proto.Position {
	return proto.Position{
		X: int32(math.Round(float64(x * float32(frame.X)))), Y: int32(math.Round(float64(y * float32(frame.Y)))),
		Width: uint16(frame.X), Height: uint16(frame.Y),
	}
}

// Layouts is the set of saved layouts.
type Layouts struct {
	Layouts []Layout `json:"layouts"`
	// Assigned maps a device serial to the layout it plays with.
	Assigned map[string]string `json:"assigned,omitempty"`
}

// Find returns the layout called name, or nil.
func (ls *Layouts) Find(name string) *Layout {
	for i := range ls.Layouts {
		if ls.Layouts[i].Name == name {
			return &ls.Layouts[i]
		}
	}
	return nil
}

// LayoutsPath is where layouts are kept: the user config dir/scry/keymaps.json.
func LayoutsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "scry", "keymaps.json"), nil
}

// LoadLayouts reads layouts from path; a missing file is an empty set.
func LoadLayouts(path string) (Layouts, error) {
	var ls Layouts
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ls, nil
	}
	if err != nil {
		return ls, err
	}
	return ls, json.Unmarshal(data, &ls)
}

// Save writes the layouts to path, atomically.
func (ls Layouts) Save(path string) error {
	data, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
