package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/hhhapz/giokit/kit"
	"github.com/hhhapz/giokit/kit/ui"

	"github.com/hhhapz/scry/scrcpy/input"
)

// keymapEditor owns the saved game-control layouts and the overlay that
// shows and edits them on the video.
type keymapEditor struct {
	// Path is where layouts are saved; set before first use (tests).
	Path string

	loaded   bool
	layouts  []*input.Layout // pointers: a live Keymap keeps one
	assigned map[string]string
	err      error

	editing  bool
	selected int // binding index, -1 for none
	dragging bool
	grab     f32.Point // pointer offset from the badge centre, in widget px

	surface   int // tag of the edit overlay
	edit      kit.Clickable
	addTap    kit.Clickable
	addJoy    kit.Clickable
	del       kit.Clickable
	newLayout kit.Clickable
	layoutSel ui.SelectState
}

func (e *keymapEditor) init() { e.selected = -1 }

func (e *keymapEditor) load() {
	if e.loaded {
		return
	}
	e.loaded = true
	if e.Path == "" {
		e.Path, e.err = input.LayoutsPath()
		if e.err != nil {
			return
		}
	}
	ls, err := input.LoadLayouts(e.Path)
	if err != nil {
		e.err = err
		return
	}
	for i := range ls.Layouts {
		e.layouts = append(e.layouts, &ls.Layouts[i])
	}
	e.assigned = ls.Assigned
	if e.assigned == nil {
		e.assigned = map[string]string{}
	}
}

func (e *keymapEditor) save() {
	ls := input.Layouts{Assigned: e.assigned}
	for _, l := range e.layouts {
		ls.Layouts = append(ls.Layouts, *l)
	}
	if e.Path != "" {
		e.err = ls.Save(e.Path)
	}
}

func (e *keymapEditor) find(name string) *input.Layout {
	for _, l := range e.layouts {
		if l.Name == name {
			return l
		}
	}
	return nil
}

// layoutFor is the layout a new session starts with: none (game controls
// start off; the toolbar switch turns them on).
func (e *keymapEditor) layoutFor(serial string) *input.Layout { return nil }

// starter is the first layout: a WASD stick and a jump button, to edit.
func starter(name string) *input.Layout {
	return &input.Layout{Name: name, Bindings: []input.Binding{
		{Kind: input.Joystick, X: 0.18, Y: 0.72, Up: "W", Left: "A", Down: "S", Right: "D", Radius: 0.1},
		{Kind: input.Tap, X: 0.85, Y: 0.75, Key: string(key.NameSpace)},
	}}
}

// enable returns the device's layout, creating a starter one if there is
// none yet.
func (e *keymapEditor) enable(serial string) *input.Layout {
	e.load()
	if l := e.find(e.assigned[serial]); l != nil {
		return l
	}
	if len(e.layouts) == 0 {
		e.layouts = append(e.layouts, starter("Default"))
		e.editing = true // a new layout: show where things are
	}
	l := e.layouts[0]
	e.assigned[serial] = l.Name
	e.save()
	return l
}

func (e *keymapEditor) deselect() { e.selected = -1 }

func (e *keymapEditor) names() []string {
	var ns []string
	for _, l := range e.layouts {
		ns = append(ns, l.Name)
	}
	return ns
}

// handle reads the editor's toolbar and overlay input.
func (e *keymapEditor) handle(gtx C, v *View, ls *liveSession) {
	l := ls.km.Layout
	if e.edit.Clicked(gtx) {
		e.editing = !e.editing
		e.selected = -1
		ls.send(ls.km.Release(ls.video.Frame())...)
	}
	if i, ok := e.layoutSel.Update(gtx); ok && i < len(e.layouts) {
		ls.send(ls.km.Release(ls.video.Frame())...)
		l = e.layouts[i]
		ls.km.Layout = l
		e.assigned[ls.serial] = l.Name
		e.selected = -1
		e.save()
	}
	if e.newLayout.Clicked(gtx) {
		ls.send(ls.km.Release(ls.video.Frame())...)
		name := fmt.Sprintf("Layout %d", len(e.layouts)+1)
		for e.find(name) != nil {
			name += "′"
		}
		l = &input.Layout{Name: name}
		e.layouts = append(e.layouts, l)
		ls.km.Layout = l
		e.assigned[ls.serial] = name
		e.editing, e.selected = true, -1
		e.save()
	}
	if e.addTap.Clicked(gtx) {
		l.Bindings = append(l.Bindings, input.Binding{Kind: input.Tap, X: 0.5, Y: 0.5})
		e.selected = len(l.Bindings) - 1 // waits for its key
		e.save()
	}
	if e.addJoy.Clicked(gtx) {
		l.Bindings = append(l.Bindings, input.Binding{Kind: input.Joystick, X: 0.2, Y: 0.7, Up: "W", Left: "A", Down: "S", Right: "D", Radius: 0.1})
		e.selected = len(l.Bindings) - 1
		e.save()
	}
	if e.del.Clicked(gtx) {
		e.remove(l)
	}
	if !e.editing {
		return
	}
	frame := ls.video.Frame()
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: &e.surface, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel},
			key.FocusFilter{Target: &e.surface},
			key.Filter{Focus: &e.surface, Optional: key.ModShift | key.ModCtrl | key.ModAlt | key.ModSuper},
		)
		if !ok {
			break
		}
		if ke, ok := ev.(key.Event); ok {
			e.key(v, ls, ke)
			continue
		}
		pe, ok := ev.(pointer.Event)
		if !ok || frame.X == 0 {
			continue
		}
		switch pe.Kind {
		case pointer.Press:
			gtx.Execute(key.FocusCmd{Tag: &e.surface}) // keys bind the selection
			e.selected, e.dragging = -1, false
			best := float32(math.MaxFloat32)
			for i, b := range l.Bindings {
				c := ls.video.FromFrame(f32.Pt(b.X*float32(frame.X), b.Y*float32(frame.Y)))
				d := dist(c, pe.Position)
				if d <= e.hitRadius(gtx, ls, b) && d < best {
					best, e.selected, e.dragging = d, i, true
					e.grab = pe.Position.Sub(c)
				}
			}
		case pointer.Drag:
			if e.dragging && e.selected >= 0 && e.selected < len(l.Bindings) {
				q, _ := ls.video.ToFrame(pe.Position.Sub(e.grab))
				b := &l.Bindings[e.selected]
				b.X = clamp01(q.X / float32(frame.X))
				b.Y = clamp01(q.Y / float32(frame.Y))
			}
		case pointer.Release, pointer.Cancel:
			if e.dragging {
				e.dragging = false
				e.save()
			}
		}
	}
}

func (e *keymapEditor) remove(l *input.Layout) {
	if e.selected < 0 || e.selected >= len(l.Bindings) {
		return
	}
	l.Bindings = append(l.Bindings[:e.selected], l.Bindings[e.selected+1:]...)
	e.selected = -1
	e.save()
}

// key handles a key while editing: Delete removes the selection, any
// other key binds the selected button.
func (e *keymapEditor) key(v *View, ls *liveSession, ev key.Event) {
	l := ls.km.Layout
	if ev.State != key.Press || l == nil || e.selected < 0 || e.selected >= len(l.Bindings) {
		return
	}
	if ev.Name == key.NameDeleteForward || ev.Name == key.NameDeleteBackward {
		e.remove(l)
		return
	}
	if ev.Name == key.NameEscape {
		e.selected = -1
		return
	}
	b := &l.Bindings[e.selected]
	if b.Kind == input.Tap {
		b.Key = string(ev.Name)
		e.save()
	}
}

func (e *keymapEditor) hitRadius(gtx C, ls *liveSession, b input.Binding) float32 {
	if b.Kind == input.Joystick {
		return max(float32(gtx.Dp(24)), joyRadius(ls, b))
	}
	return float32(gtx.Dp(24))
}

// joyRadius is a joystick's throw in widget pixels.
func joyRadius(ls *liveSession, b input.Binding) float32 {
	frame := ls.video.Frame()
	if frame.X == 0 {
		return 0
	}
	r := b.Radius
	if r <= 0 {
		r = 0.1
	}
	scale := float32(ls.video.Content().Dx()) / float32(frame.X)
	return r * float32(min(frame.X, frame.Y)) * scale
}

func (e *keymapEditor) toolbar(gtx C, v *View, ls *liveSession) D {
	th := v.Theme
	editLabel := "Edit layout"
	if e.editing {
		editLabel = "Done"
	}
	l := ls.km.Layout
	sel := 0
	for i, x := range e.layouts {
		if x == l {
			sel = i
		}
	}
	children := []kit.W{
		func(gtx C) D {
			if len(e.layouts) < 2 {
				return th.Text(gtx, l.Name, th.Type.Body, th.Fg, kit.Medium)
			}
			return kit.Fixed(180, func(gtx C) D {
				return ui.Select{ID: "keymap/layout", Options: e.names(), Selected: sel}.Layout(gtx, th, &e.layoutSel)
			})(gtx)
		},
		func(gtx C) D {
			return ui.Button{ID: "keymap/new", Text: "New layout", Style: ui.Ghost, Size: kit.ControlSmall}.Layout(gtx, th, &e.newLayout)
		},
		func(gtx C) D {
			return ui.Button{ID: "keymap/edit", Text: editLabel, Style: ui.Primary, Size: kit.ControlSmall}.Layout(gtx, th, &e.edit)
		},
	}
	if e.editing {
		children = append(children,
			func(gtx C) D {
				return ui.Button{ID: "keymap/add-tap", Text: "Add button", Icon: kit.IconPlus, Style: ui.Ghost, Size: kit.ControlSmall}.Layout(gtx, th, &e.addTap)
			},
			func(gtx C) D {
				return ui.Button{ID: "keymap/add-joystick", Text: "Add joystick", Icon: kit.IconPlus, Style: ui.Ghost, Size: kit.ControlSmall}.Layout(gtx, th, &e.addJoy)
			},
			func(gtx C) D {
				return th.Disable(gtx, e.selected < 0, func(gtx C) D {
					return ui.Button{ID: "keymap/delete", Text: "Delete", Style: ui.Danger, Size: kit.ControlSmall}.Layout(gtx, th, &e.del)
				})
			},
			func(gtx C) D {
				hint := "Drag a control to move it. Select a button, then press the key it should use."
				if e.selected >= 0 && e.selected < len(l.Bindings) && l.Bindings[e.selected].Kind == input.Tap && l.Bindings[e.selected].Key == "" {
					hint = "Press the key for this button."
				}
				return th.Text(gtx, hint, th.Type.Small, th.Muted, kit.Regular)
			},
		)
	}
	if e.err != nil {
		children = append(children, func(gtx C) D {
			return th.Text(gtx, "Layouts: "+e.err.Error(), th.Type.Small, th.Warn, kit.Regular)
		})
	}
	return kit.Flow(gtx, kit.Space2, kit.Space1, children...)
}

// overlay draws the bindings on the video: faint while playing, solid and
// draggable while editing.
func (e *keymapEditor) overlay(gtx C, v *View, ls *liveSession) {
	th := v.Theme
	l := ls.km.Layout
	frame := ls.video.Frame()
	if l == nil || frame.X == 0 {
		return
	}
	if e.editing {
		area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
		kit.TagLabel(gtx, "keymap/surface", "Game controls editor: drag controls, press a key to bind")
		event.Op(gtx.Ops, &e.surface)
		pointer.CursorGrab.Add(gtx.Ops)
		area.Pop()
	}
	alpha := uint8(110)
	if e.editing {
		alpha = 230
	}
	for i, b := range l.Bindings {
		c := ls.video.FromFrame(f32.Pt(b.X*float32(frame.X), b.Y*float32(frame.Y)))
		sel := e.editing && i == e.selected
		ring := th.Accent
		if sel {
			ring = th.Fg
		}
		if b.Kind == input.Joystick {
			r := joyRadius(ls, b)
			circle(gtx, c, r, kit.WithAlpha(th.Panel, alpha/2))
			ringAt(gtx, c, r, float32(gtx.Dp(2)), kit.WithAlpha(ring, alpha))
			for _, k := range []struct {
				name string
				dx   float32
				dy   float32
			}{{b.Up, 0, -1}, {b.Left, -1, 0}, {b.Down, 0, 1}, {b.Right, 1, 0}} {
				if k.name != "" {
					label(gtx, th, c.Add(f32.Pt(k.dx*r*0.65, k.dy*r*0.65)), keyLabel(k.name))
				}
			}
			continue
		}
		r := float32(gtx.Dp(18))
		circle(gtx, c, r, kit.WithAlpha(th.AccentBg, alpha))
		ringAt(gtx, c, r, float32(gtx.Dp(2)), kit.WithAlpha(ring, alpha))
		name := keyLabel(b.Key)
		if name == "" {
			name = "?"
		}
		label(gtx, th, c, name)
	}
}

// keyLabel is a short readable name for a Gio key name.
func keyLabel(n string) string {
	switch key.Name(n) {
	case key.NameReturn, key.NameEnter:
		return "Enter"
	case key.NameLeftArrow:
		return "Left"
	case key.NameRightArrow:
		return "Right"
	case key.NameUpArrow:
		return "Up"
	case key.NameDownArrow:
		return "Down"
	case key.NameDeleteBackward:
		return "Bksp"
	case key.NameDeleteForward:
		return "Del"
	case key.NameEscape:
		return "Esc"
	case key.NameHome:
		return "Home"
	case key.NameEnd:
		return "End"
	case key.NamePageUp:
		return "PgUp"
	case key.NamePageDown:
		return "PgDn"
	case key.NameCommand:
		return "Cmd"
	}
	return n
}

func dist(a, b f32.Point) float32 {
	return float32(math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)))
}

func clamp01(v float32) float32 { return max(0, min(1, v)) }

func circle(gtx C, c f32.Point, r float32, col color.NRGBA) {
	rect := image.Rect(int(c.X-r), int(c.Y-r), int(c.X+r), int(c.Y+r))
	paint.FillShape(gtx.Ops, col, clip.Ellipse(rect).Op(gtx.Ops))
}

// ringAt draws a ring as the difference of two ellipses (no strokes).
func ringAt(gtx C, c f32.Point, r, w float32, col color.NRGBA) {
	outer := image.Rect(int(c.X-r), int(c.Y-r), int(c.X+r), int(c.Y+r))
	inner := image.Rect(int(c.X-r+w), int(c.Y-r+w), int(c.X+r-w), int(c.Y+r-w))
	var p clip.Path
	p.Begin(gtx.Ops)
	ellipsePath(&p, outer, false)
	ellipsePath(&p, inner, true)
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: p.End()}.Op())
}

// ellipsePath adds an ellipse approximated by cubic Béziers, reversed for
// a hole.
func ellipsePath(p *clip.Path, r image.Rectangle, reverse bool) {
	cx, cy := float32(r.Min.X+r.Max.X)/2, float32(r.Min.Y+r.Max.Y)/2
	rx, ry := float32(r.Dx())/2, float32(r.Dy())/2
	const k = 0.5522847498
	pt := f32.Pt
	pts := [][4]f32.Point{
		{pt(cx+rx, cy), pt(cx+rx, cy+k*ry), pt(cx+k*rx, cy+ry), pt(cx, cy+ry)},
		{pt(cx, cy+ry), pt(cx-k*rx, cy+ry), pt(cx-rx, cy+k*ry), pt(cx-rx, cy)},
		{pt(cx-rx, cy), pt(cx-rx, cy-k*ry), pt(cx-k*rx, cy-ry), pt(cx, cy-ry)},
		{pt(cx, cy-ry), pt(cx+k*rx, cy-ry), pt(cx+rx, cy-k*ry), pt(cx+rx, cy)},
	}
	if reverse {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
		for i := range pts {
			pts[i] = [4]f32.Point{pts[i][3], pts[i][2], pts[i][1], pts[i][0]}
		}
	}
	p.MoveTo(pts[0][0])
	for _, s := range pts {
		p.CubeTo(s[1], s[2], s[3])
	}
	p.Close()
}

// label draws text centred on c, on an opaque pill so it reads over any
// video.
func label(gtx C, th *kit.Theme, c f32.Point, s string) {
	m := op.Record(gtx.Ops)
	g := gtx
	g.Constraints.Min = image.Point{}
	d := th.Text(g, s, th.Type.Small, th.Fg, kit.SemiBold)
	call := m.Stop()
	pad := gtx.Dp(4)
	x, y := int(c.X)-d.Size.X/2, int(c.Y)-d.Size.Y/2
	pill := image.Rect(x-pad, y-pad/2, x+d.Size.X+pad, y+d.Size.Y+pad/2)
	rr := pill.Dy() / 2
	paint.FillShape(gtx.Ops, th.Bg, clip.UniformRRect(pill, rr).Op(gtx.Ops))
	off := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	off.Pop()
}
