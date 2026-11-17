package ui

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gioui.org/f32"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/hhhapz/giokit/kit"
	"github.com/hhhapz/giokit/kit/ui"
	"github.com/hhhapz/giokit/media"

	"github.com/hhhapz/scry/scrcpy"
	"github.com/hhhapz/scry/scrcpy/client"
	"github.com/hhhapz/scry/scrcpy/fake"
	"github.com/hhhapz/scry/scrcpy/input"
	"github.com/hhhapz/scry/scrcpy/proto"
)

// liveSession is a session mirrored inside scry: scry runs the scrcpy
// protocol itself, decodes the video into a paint.Stream and sends input
// over the control socket.
type liveSession struct {
	serial string
	opts   scrcpy.Options
	conn   *client.Conn // nil while connecting
	stream *paint.Stream
	video  ui.VideoState
	cancel bool         // Stop pressed while connecting
	fake   *fake.Device // the demo device behind a demo session

	// Reader goroutine only.
	dec    *media.Decoder
	failed bool // decoding failed once (logged)

	// Shared: written by the reader goroutine, read by the UI.
	frames   atomic.Int64
	width    atomic.Int32
	height   atomic.Int32
	codec    atomic.Value // string
	hw       atomic.Bool
	zeroCopy atomic.Bool

	// UI goroutine.
	fps        float64
	fpsFrames  int64
	fpsAt      time.Time
	kb         input.Keyboard
	mouse      input.Mouse
	km         input.Keymap
	buttons    pointer.Buttons
	focused    bool
	screenOff  bool
	audio      *scrcpy.Session
	clipSeq    uint64
	clipOut    *string // device clipboard to write to the desktop
	pasteHeld  bool    // Ctrl+V went to the clipboard, not as a key
	resizeTo   image.Point
	resizeSent image.Point
	resizeAt   time.Time
}

// screenPage is the Screen tab's widget state.
type screenPage struct {
	buttons kit.Pool[kit.Clickable]
	game    kit.Clickable
	editor  keymapEditor
}

func (p *screenPage) init() { p.editor.init() }

// toolbar actions: id, label, and what they send.
var screenActions = []struct {
	id, label string
	keycode   uint32 // a key to press and release, or 0
}{
	{"back", "Back", proto.KeycodeBack},
	{"home", "Home", proto.KeycodeHome},
	{"recents", "Recents", proto.KeycodeAppSwitch},
	{"notifications", "Notifications", 0},
	{"vol-down", "Vol −", proto.KeycodeVolumeDown},
	{"vol-up", "Vol +", proto.KeycodeVolumeUp},
	{"rotate", "Rotate", 0},
	{"power", "Screen off", 0},
}

func mediaCodec(name string) (media.Codec, bool) {
	switch name {
	case "h264":
		return media.H264, true
	case "h265":
		return media.H265, true
	case "av1":
		return media.AV1, true
	case "vp8":
		return media.VP8, true
	case "vp9":
		return media.VP9, true
	}
	return 0, false
}

func (ls *liveSession) send(ms ...proto.Control) {
	if ls.conn == nil {
		return
	}
	for _, m := range ms {
		ls.conn.Send(m)
	}
}

func keyMode(o scrcpy.Options) input.KeyMode {
	return input.Mixed
}

// startLive connects an embedded session to the selected device.
func (v *View) startLive(serial string) {
	o := *v.options()
	ls := &liveSession{serial: serial, opts: o, stream: paint.NewStream()}
	ls.stream.OnFrame = v.invalidate
	ls.kb.Mode = keyMode(o)
	ls.mouse.Hover = true
	ls.km.Layout = v.screen.editor.layoutFor(serial)
	v.live[serial] = ls
	v.tab = tabScreen
	logf := func(level scrcpy.Level, format string, a ...any) {
		line := scrcpy.LogLine{Level: level, Source: "scry", Text: fmt.Sprintf(format, a...)}
		v.post(func() { v.log(serial, line) })
	}

	h := client.Handler{
		Codec: func(name string) {
			ls.codec.Store(name)
			c, ok := mediaCodec(name)
			if !ok {
				logf(scrcpy.Error, "unsupported video codec %q", name)
				return
			}
			opt := media.Options{DMABuf: runtime.GOOS == "linux"}
			if os.Getenv("SCRY_HWDEC") == "off" { // escape hatch for misbehaving drivers
				opt = media.Options{Hardware: media.HardwareOff}
			}
			dec, err := media.NewDecoder(c, opt)
			if err != nil {
				logf(scrcpy.Error, "video decoder: %v", err)
				return
			}
			ls.dec = dec
			ls.hw.Store(dec.Hardware())
			where := "in software"
			if dec.Hardware() {
				where = "on the GPU (VA-API)"
			}
			logf(scrcpy.Info, "decoding %s %s", name, where)
		},
		Session: func(s proto.Session) {
			ls.width.Store(int32(s.Width))
			ls.height.Store(int32(s.Height))
			logf(scrcpy.Info, "video %d×%d", s.Width, s.Height)
		},
		Packet: func(p proto.Packet) {
			if ls.dec == nil {
				return
			}
			err := ls.dec.Decode(media.Packet{Data: p.Data, PTS: p.PTS, Key: p.Key, Config: p.Config}, func(f media.Frame) {
				ls.zeroCopy.Store(f.DMABuf != nil)
				ls.stream.Submit(f.StreamFrame())
				ls.frames.Add(1)
			})
			if ls.stream.ImportState() == paint.ImportUnsupported && ls.dec.DMABufError() == nil {
				ls.dec.DisableDMABuf()
				logf(scrcpy.Warn, "this renderer cannot draw GPU frames directly; copying frames instead")
			}
			if err != nil && !ls.failed {
				ls.failed = true
				logf(scrcpy.Error, "decode: %v", err)
			}
		},
		Device: func(m proto.DeviceMessage) {
			if m.Type == proto.DeviceClipboard {
				text := m.Text
				v.post(func() { ls.clipOut = &text })
				v.invalidate()
			}
		},
		Stopped: func() {
			if ls.dec != nil {
				ls.dec.Close()
			}
		},
		Closed: func(err error) {
			v.post(func() { v.liveEnded(ls, err) })
			v.invalidate()
		},
	}

	if v.isDemo(serial) {
		d := &fake.Device{Name: v.title(*v.device()), File: v.demos[serial], Loop: true, Realtime: true}
		ls.fake = d
		conn, err := d.Connect(h)
		v.liveConnected(ls, conn, err)
		return
	}
	v.log(serial, scrcpy.LogLine{Level: scrcpy.Info, Source: "you", Text: "mirror in scry: scrcpy-server " + strings.Join(client.ServerArgs(o, 0, false)[1:], " ")})
	v.status = "Connecting…"
	kit.Spawn(v.app, func(ctx context.Context) (*client.Conn, error) {
		var parser scrcpy.LogParser
		return client.Connect(ctx, client.Config{
			Serial: serial, Options: o,
			Log: func(line string) {
				v.post(func() { v.log(serial, parser.Parse(line)) })
			},
		}, h)
	}, func(conn *client.Conn, err error) { v.liveConnected(ls, conn, err) })
	if !o.NoAudio {
		v.startAudio(ls)
	}
}

func (v *View) liveConnected(ls *liveSession, conn *client.Conn, err error) {
	if err != nil {
		v.log(ls.serial, scrcpy.LogLine{Level: scrcpy.Error, Source: "scry", Text: "could not mirror: " + err.Error()})
		v.toasts.Push(kit.StatusBad, "Could not start mirroring", err.Error())
		v.stopAudio(ls)
		delete(v.live, ls.serial)
		return
	}
	if ls.cancel {
		conn.Close()
		return
	}
	ls.conn = conn
	v.status = "Mirroring " + conn.DeviceName
	v.log(ls.serial, scrcpy.LogLine{Level: scrcpy.Info, Source: "scry", Text: "connected to " + conn.DeviceName})
}

// stopLive ends an embedded session; liveEnded cleans up when it has.
func (v *View) stopLive(ls *liveSession) {
	if ls.conn == nil {
		ls.cancel = true
		v.stopAudio(ls)
		delete(v.live, ls.serial)
		return
	}
	ls.send(ls.km.Release(ls.video.Frame())...)
	ls.send(ls.kb.Release()...)
	ls.conn.Close()
}

func (v *View) liveEnded(ls *liveSession, err error) {
	v.stopAudio(ls)
	if v.live[ls.serial] == ls {
		delete(v.live, ls.serial)
	}
	if err != nil {
		v.log(ls.serial, scrcpy.LogLine{Level: scrcpy.Error, Source: "scry", Text: "mirroring ended: " + err.Error()})
		v.toasts.Push(kit.StatusWarn, "Mirroring ended", err.Error())
	} else {
		v.log(ls.serial, scrcpy.LogLine{Level: scrcpy.Info, Source: "scry", Text: "mirroring stopped"})
	}
	v.status = "Ready"
}

// startAudio plays the device's audio through a second, video-less scrcpy:
// the server allows several sessions, and scrcpy's player is good.
func (v *View) startAudio(ls *liveSession) {
	args := []string{"--serial=" + ls.serial, "--no-video", "--no-control", "--no-window"}
	if ls.opts.AudioSource != "" {
		args = append(args, "--audio-source="+ls.opts.AudioSource)
	}
	serial := ls.serial
	var parser scrcpy.LogParser
	s, err := scrcpy.Start(args, func(line string) {
		v.post(func() {
			l := parser.Parse(line)
			l.Source = "audio"
			v.log(serial, l)
		})
	})
	if err != nil {
		v.log(serial, scrcpy.LogLine{Level: scrcpy.Warn, Source: "audio", Text: "no audio: " + err.Error()})
		return
	}
	ls.audio = s
}

func (v *View) stopAudio(ls *liveSession) {
	if ls.audio != nil {
		ls.audio.Stop()
		ls.audio = nil
	}
}

// handleScreen reads the Screen tab's input (rule: handle, then draw).
func (v *View) handleScreen(gtx C) {
	ls := v.live[v.selected]
	if ls == nil {
		return
	}
	p := &v.screen
	for _, a := range screenActions {
		if !p.buttons.Get(a.id).Clicked(gtx) {
			continue
		}
		switch a.id {
		case "notifications":
			ls.send(proto.ExpandNotificationPanel())
		case "rotate":
			ls.send(proto.RotateDevice())
		case "power":
			ls.screenOff = !ls.screenOff
			ls.send(proto.SetDisplayPower(!ls.screenOff))
		default:
			ls.send(proto.InjectKeycode(proto.KeyDown, a.keycode, 0, 0), proto.InjectKeycode(proto.KeyUp, a.keycode, 0, 0))
		}
	}
	if p.game.Clicked(gtx) {
		ls.send(ls.km.Release(ls.video.Frame())...)
		if ls.km.Layout == nil {
			ls.km.Layout = p.editor.enable(ls.serial)
		} else {
			ls.km.Layout = nil
			p.editor.editing = false
		}
	}
	if ls.km.Layout != nil {
		p.editor.handle(gtx, v, ls)
	}
	if ls.clipOut != nil {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(*ls.clipOut))})
		ls.clipOut = nil
	}
	if now := gtx.Now; now.Sub(ls.fpsAt) >= time.Second {
		n := ls.frames.Load()
		if !ls.fpsAt.IsZero() {
			ls.fps = float64(n-ls.fpsFrames) / now.Sub(ls.fpsAt).Seconds()
		}
		ls.fpsFrames, ls.fpsAt = n, now
	}
	if ls.opts.Kind == scrcpy.AppWindow && ls.opts.Flex && ls.resizeTo != ls.resizeSent && gtx.Now.Sub(ls.resizeAt) > 300*time.Millisecond {
		ls.resizeSent = ls.resizeTo
		ls.send(proto.ResizeDisplay(uint16(ls.resizeTo.X), uint16(ls.resizeTo.Y)))
	}
	v.handleVideoInput(gtx, ls)
}

// handleVideoInput forwards pointer and keyboard input on the video.
func (v *View) handleVideoInput(gtx C, ls *liveSession) {
	frame := ls.video.Frame()
	position := func(p f32.Point) (proto.Position, bool) {
		q, ok := ls.video.ToFrame(p)
		x := max(0, min(float32(frame.X-1), q.X))
		y := max(0, min(float32(frame.Y-1), q.Y))
		return proto.Position{X: int32(x), Y: int32(y), Width: uint16(frame.X), Height: uint16(frame.Y)}, ok
	}
	editing := v.screen.editor.editing && ls.km.Layout != nil
	mouseOn := ls.opts.Mouse != "disabled" && !ls.opts.ReadOnly
	keysOn := ls.opts.Keyboard != "disabled" && !ls.opts.ReadOnly
	big := pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}
	filters := []event.Filter{
		pointer.Filter{Target: ls, Kinds: pointer.Press | pointer.Release | pointer.Drag | pointer.Move | pointer.Scroll | pointer.Cancel, ScrollX: big, ScrollY: big},
		key.FocusFilter{Target: ls},
		key.Filter{Focus: ls, Optional: key.ModShift | key.ModCtrl | key.ModAlt | key.ModSuper | key.ModCommand},
		transfer.TargetFilter{Target: ls, Type: "application/text"},
	}
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			pos, inside := position(e.Position)
			if e.Kind == pointer.Press {
				gtx.Execute(key.FocusCmd{Tag: ls})
			}
			if editing {
				if e.Kind == pointer.Press {
					v.screen.editor.deselect()
				}
				continue
			}
			if !mouseOn || frame.X == 0 {
				continue
			}
			switch e.Kind {
			case pointer.Press:
				changed := e.Buttons &^ ls.buttons
				ls.buttons = e.Buttons
				if !inside {
					continue
				}
				for _, b := range splitButtons(changed) {
					ls.send(ls.mouse.Press(pos, b, e.Modifiers&key.ModCtrl != 0, e.Modifiers&key.ModShift != 0)...)
				}
			case pointer.Release:
				changed := ls.buttons &^ e.Buttons
				ls.buttons = e.Buttons
				for _, b := range splitButtons(changed) {
					ls.send(ls.mouse.Release(pos, b)...)
				}
			case pointer.Drag, pointer.Move:
				if inside || ls.buttons != 0 {
					ls.send(ls.mouse.Move(pos)...)
				}
			case pointer.Scroll:
				if inside {
					// Gio reports pixels; a wheel notch is about 50px here.
					step := float32(gtx.Dp(50))
					ls.send(ls.mouse.Scroll(pos, e.Scroll.X/step, e.Scroll.Y/step)...)
				}
			case pointer.Cancel:
				ls.buttons = 0
				ls.send(ls.mouse.Cancel(pos)...)
			}
		case key.FocusEvent:
			ls.focused = e.Focus
			if !e.Focus {
				ls.send(ls.kb.Release()...)
				ls.send(ls.km.Release(frame)...)
			}
		case key.Event:
			if editing {
				v.screen.editor.key(v, ls, e)
				continue
			}
			if !keysOn {
				continue
			}
			if ms, ok := ls.km.Key(e, frame); ok {
				ls.send(ms...)
				continue
			}
			// Ctrl+V: put the desktop clipboard on the device and paste it,
			// as scrcpy does, instead of pasting the device's own clipboard.
			if e.Name == "V" && e.Modifiers&key.ModCtrl != 0 && !ls.opts.NoClipboardSync {
				if e.State == key.Press {
					gtx.Execute(clipboard.ReadCmd{Tag: ls})
					ls.pasteHeld = true
				}
				continue
			}
			if e.Name == "V" && e.State == key.Release && ls.pasteHeld {
				ls.pasteHeld = false
				continue
			}
			if m, ok := ls.kb.Key(e); ok {
				ls.send(m)
			}
		case key.EditEvent:
			if editing || !keysOn || ls.km.Layout != nil {
				continue // game controls own the keys
			}
			if m, ok := ls.kb.Text(e.Text); ok {
				ls.send(m)
			}
		case transfer.DataEvent:
			r := e.Open()
			b, _ := io.ReadAll(io.LimitReader(r, proto.MaxClipboardText))
			r.Close()
			ls.clipSeq++
			ls.send(proto.SetClipboard(ls.clipSeq, string(b), true))
		}
	}
}

// splitButtons lists the input buttons in a Gio button set.
func splitButtons(b pointer.Buttons) []input.Button {
	var out []input.Button
	for _, m := range []struct {
		g pointer.Buttons
		b input.Button
	}{
		{pointer.ButtonPrimary, input.Primary},
		{pointer.ButtonSecondary, input.Secondary},
		{pointer.ButtonTertiary, input.Tertiary},
		{pointer.ButtonQuaternary, input.Back},
		{pointer.ButtonQuinary, input.Forward},
	} {
		if b&m.g != 0 {
			out = append(out, m.b)
		}
	}
	return out
}

// screenLayout draws the Screen tab.
func (v *View) screenLayout(gtx C) D {
	th := v.Theme
	gtx.Constraints.Min = gtx.Constraints.Max
	ls := v.live[v.selected]
	if ls == nil {
		body := "Press Start to mirror the device here. To use scrcpy's own window instead, turn on Settings → Window → Open in a separate scrcpy window."
		if o := v.options(); !o.Embeddable() {
			body = "This session kind runs in scrcpy itself. Choose Mirror, App window or Camera to mirror inside scry."
		}
		return ui.EmptyState{ID: "screen-empty", Title: "Not mirroring", Body: body}.Layout(gtx, th)
	}
	return kit.Col(gtx,
		kit.Rigid(func(gtx C) D { return v.screenToolbar(gtx, ls) }),
		kit.Rigid(kit.HLine(th.Divider)),
		kit.Flexed(1, func(gtx C) D { return v.screenVideo(gtx, ls) }),
	)
}

func (v *View) screenToolbar(gtx C, ls *liveSession) D {
	th := v.Theme
	p := &v.screen
	return th.Bar(gtx, 8, 16, func(gtx C) D {
		return kit.Col(gtx,
			kit.Rigid(func(gtx C) D {
				var children []kit.W
				for _, a := range screenActions {
					label := a.label
					if a.id == "power" && ls.screenOff {
						label = "Screen on"
					}
					children = append(children, func(gtx C) D {
						return th.Disable(gtx, ls.conn == nil || ls.opts.ReadOnly, func(gtx C) D {
							return ui.Button{ID: "screen/" + a.id, Text: label, Style: ui.Ghost, Size: kit.ControlSmall}.Layout(gtx, th, p.buttons.Get(a.id))
						})
					})
				}
				children = append(children, func(gtx C) D {
					return layout.Inset{Left: 12}.Layout(gtx, func(gtx C) D {
						return th.Disable(gtx, ls.opts.ReadOnly, func(gtx C) D {
							return ui.Switch{ID: "screen/game", Label: "Game controls", On: ls.km.Layout != nil}.Layout(gtx, th, &p.game)
						})
					})
				})
				return kit.Flow(gtx, kit.Space1, kit.Space1, children...)
			}),
			kit.Rigid(func(gtx C) D {
				if ls.km.Layout == nil {
					return D{}
				}
				return layout.Inset{Top: 6}.Layout(gtx, func(gtx C) D { return p.editor.toolbar(gtx, v, ls) })
			}),
			kit.Rigid(kit.Gap(0, 4)),
			kit.Rigid(func(gtx C) D {
				return th.Text(gtx, v.screenStatus(ls), th.Type.Small, th.Muted, kit.Regular)
			}),
		)
	})
}

// screenStatus is the stats line: codec, size, decoder path, frame rate.
func (v *View) screenStatus(ls *liveSession) string {
	if ls.conn == nil {
		return "Connecting…"
	}
	var parts []string
	if c, _ := ls.codec.Load().(string); c != "" {
		parts = append(parts, strings.ToUpper(c))
	}
	if w, h := ls.width.Load(), ls.height.Load(); w > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", w, h))
	}
	switch {
	case ls.zeroCopy.Load():
		parts = append(parts, "GPU decode, zero-copy")
	case ls.hw.Load():
		parts = append(parts, "GPU decode")
	case ls.frames.Load() > 0:
		parts = append(parts, "software decode")
	}
	parts = append(parts, fmt.Sprintf("%.0f fps", ls.fps))
	if ls.focused {
		parts = append(parts, "keyboard goes to the device (click outside to stop)")
	} else if !ls.opts.ReadOnly {
		parts = append(parts, "click the screen to type into it")
	}
	return strings.Join(parts, " · ")
}

func (v *View) screenVideo(gtx C, ls *liveSession) D {
	th := v.Theme
	gtx.Constraints.Min = gtx.Constraints.Max
	size := gtx.Constraints.Max
	if size != ls.resizeTo && ls.opts.Flex {
		ls.resizeTo, ls.resizeAt = size, gtx.Now
	}
	return kit.Fill(gtx, th.Panel, func(gtx C) D {
		fit := ui.FitContain
		if ls.opts.Kind == scrcpy.AppWindow && ls.opts.Flex {
			fit = ui.FitNone
		}
		d := ui.Video{ID: "screen/video", Stream: ls.stream, Fit: fit, Label: v.title(*v.device()) + " screen"}.Layout(gtx, th, &ls.video)
		// The input area: the whole video widget. While the keymap editor
		// covers it, the editor takes pointer and keys instead.
		if !(v.screen.editor.editing && ls.km.Layout != nil) {
			area := clip.Rect{Max: size}.Push(gtx.Ops)
			kit.TagLabel(gtx, "screen/input", "Device screen: click to control, type to send keys")
			event.Op(gtx.Ops, ls)
			if ls.frames.Load() > 0 && !ls.opts.ReadOnly {
				pointer.CursorPointer.Add(gtx.Ops)
			}
			area.Pop()
		}
		if ls.focused {
			r := ls.video.Content().Intersect(image.Rectangle{Max: size})
			kit.Border(gtx, r, 0, gtx.Dp(2), th.Accent)
		}
		if ls.km.Layout != nil {
			v.screen.editor.overlay(gtx, v, ls)
		}
		if ls.frames.Load() == 0 {
			layout.Center.Layout(gtx, func(gtx C) D { return th.Spinner(gtx, 28) })
		}
		return d
	})
}
