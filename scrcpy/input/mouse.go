package input

import "github.com/hhhapz/scry/scrcpy/proto"

// Button is a mouse button.
type Button uint32

const (
	Primary   = Button(proto.ButtonPrimary)
	Secondary = Button(proto.ButtonSecondary)
	Tertiary  = Button(proto.ButtonTertiary)
	Back      = Button(proto.ButtonBack)    // the 4th button
	Forward   = Button(proto.ButtonForward) // the 5th button
)

// Mouse translates mouse input in video coordinates, with scrcpy's default
// bindings for an SDK mouse ("bhsn:++++"): right click is BACK (or screen
// on), middle click HOME, the 4th button RECENTS, the 5th expands the
// notification panel. With Shift held, every button goes to the device as
// a mouse button instead. Ctrl+drag pinches and rotates around the centre.
type Mouse struct {
	// Hover forwards motion without buttons (scrcpy's default; --no-mouse-hover turns it off).
	Hover bool

	buttons uint32 // forwarded buttons held
	pinch   bool   // a Ctrl+drag pinch is in progress
	bound   Button // buttons held whose binding (BACK, HOME...) fired on press
}

// mirror is p reflected through the frame centre: the second pinch finger.
func mirror(p proto.Position) proto.Position {
	q := p
	q.X = int32(p.Width) - p.X
	q.Y = int32(p.Height) - p.Y
	return q
}

// Press handles a button press at pos. ctrl and shift are the modifiers held.
func (m *Mouse) Press(pos proto.Position, b Button, ctrl, shift bool) []proto.Control {
	if b == Primary && ctrl && !shift && m.buttons == 0 {
		m.pinch = true
		return []proto.Control{
			proto.InjectTouch(proto.MotionDown, proto.PointerGenericFinger, pos, 1, 0, 0),
			proto.InjectTouch(proto.MotionDown, proto.PointerVirtualFinger, mirror(pos), 1, 0, 0),
		}
	}
	if b != Primary && !shift {
		m.bound |= b
		switch b {
		case Secondary:
			return []proto.Control{proto.BackOrScreenOn(proto.KeyDown)}
		case Tertiary:
			return []proto.Control{proto.InjectKeycode(proto.KeyDown, proto.KeycodeHome, 0, 0)}
		case Back:
			return []proto.Control{proto.InjectKeycode(proto.KeyDown, proto.KeycodeAppSwitch, 0, 0)}
		case Forward:
			return []proto.Control{proto.ExpandNotificationPanel()}
		}
		return nil
	}
	m.buttons |= uint32(b)
	return []proto.Control{proto.InjectTouch(proto.MotionDown, proto.PointerMouse, pos, 1, uint32(b), m.buttons)}
}

// Release handles a button release at pos.
func (m *Mouse) Release(pos proto.Position, b Button) []proto.Control {
	if m.pinch && b == Primary {
		m.pinch = false
		return []proto.Control{
			proto.InjectTouch(proto.MotionUp, proto.PointerGenericFinger, pos, 0, 0, 0),
			proto.InjectTouch(proto.MotionUp, proto.PointerVirtualFinger, mirror(pos), 0, 0, 0),
		}
	}
	if m.bound&b != 0 {
		m.bound &^= b
		switch b {
		case Secondary:
			return []proto.Control{proto.BackOrScreenOn(proto.KeyUp)}
		case Tertiary:
			return []proto.Control{proto.InjectKeycode(proto.KeyUp, proto.KeycodeHome, 0, 0)}
		case Back:
			return []proto.Control{proto.InjectKeycode(proto.KeyUp, proto.KeycodeAppSwitch, 0, 0)}
		}
		return nil
	}
	if m.buttons&uint32(b) == 0 {
		return nil // pressed outside the video, or already released
	}
	m.buttons &^= uint32(b)
	return []proto.Control{proto.InjectTouch(proto.MotionUp, proto.PointerMouse, pos, 0, uint32(b), m.buttons)}
}

// Move handles motion: a drag while buttons are held, else a hover.
func (m *Mouse) Move(pos proto.Position) []proto.Control {
	switch {
	case m.pinch:
		return []proto.Control{
			proto.InjectTouch(proto.MotionMove, proto.PointerGenericFinger, pos, 1, 0, 0),
			proto.InjectTouch(proto.MotionMove, proto.PointerVirtualFinger, mirror(pos), 1, 0, 0),
		}
	case m.buttons != 0:
		return []proto.Control{proto.InjectTouch(proto.MotionMove, proto.PointerMouse, pos, 1, 0, m.buttons)}
	case m.Hover:
		return []proto.Control{proto.InjectTouch(proto.MotionHoverMove, proto.PointerMouse, pos, 0, 0, 0)}
	}
	return nil
}

// Scroll scrolls at pos; h and v are in wheel steps (positive: right, down
// on the desktop), sent in Android's sense (positive: up).
func (m *Mouse) Scroll(pos proto.Position, h, v float32) []proto.Control {
	if h == 0 && v == 0 {
		return nil
	}
	return []proto.Control{proto.InjectScroll(pos, -h, -v, m.buttons)}
}

// Cancel lifts everything held: call it when the pointer is lost.
func (m *Mouse) Cancel(pos proto.Position) []proto.Control {
	var out []proto.Control
	if m.pinch {
		out = append(out, m.Release(pos, Primary)...)
	}
	for _, b := range []Button{Primary, Secondary, Tertiary, Back, Forward} {
		if m.buttons&uint32(b) != 0 || m.bound&b != 0 {
			out = append(out, m.Release(pos, b)...)
		}
	}
	return out
}
