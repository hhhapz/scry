// Package ui is scry's giokit front end. It edits scrcpy.Options and starts
// sessions; everything it shows comes from the adb and scrcpy packages and
// the schema in settings.go, so it can be replaced wholesale.
package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/hhhapz/giokit/kit"
	"github.com/hhhapz/giokit/kit/ui"

	"github.com/hhhapz/scry/adb"
	"github.com/hhhapz/scry/scrcpy"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

// View is the whole window.
type View struct {
	Theme *kit.Theme
	app   *kit.App // nil in tests

	devices  []adb.Device
	selected string                     // serial
	opts     map[string]*scrcpy.Options // per device
	sessions map[string]*scrcpy.Session
	info     map[string]adb.Info     // what each authorized device said about itself
	live     map[string]*liveSession // embedded sessions, by serial
	demos    map[string]string       // demo devices: serial → video file ("" for the built-in clip)
	status   string

	postMu sync.Mutex
	posts  []func() // without an app (tests): run at the start of the next frame

	// widget state
	nav      kit.Pool[kit.Clickable]
	navGroup kit.FocusGroup
	refresh  kit.Clickable
	add      kit.Clickable
	start    kit.Clickable
	kind     ui.TabsState
	kindSel  ui.SelectState // replaces the tabs when they do not fit
	fps      ui.TabsState
	bitrate  ui.SliderState
	appName  widget.Editor
	toggles  kit.Pool[kit.Clickable]
	selects  kit.Pool[ui.SelectState]
	scroll   kit.List // settings
	tab      int      // tabSettings, tabScreen, tabLogs
	tabs     ui.TabsState
	screen   screenPage
	logs     logsPage
	toasts   ui.Toasts
}

// New makes the view. With a non-nil app it lists adb devices at once.
func New(app *kit.App) *View {
	th := kit.Dark()
	v := &View{Theme: th, app: app,
		opts:     map[string]*scrcpy.Options{},
		sessions: map[string]*scrcpy.Session{},
		info:     map[string]adb.Info{},
		live:     map[string]*liveSession{},
		demos:    map[string]string{},
	}
	v.appName.SingleLine = true
	v.scroll.Axis = layout.Vertical
	v.logs.init()
	v.screen.init()
	if app != nil {
		v.Refresh()
	}
	return v
}

// Refresh re-lists devices in the background.
func (v *View) Refresh() {
	v.status = "Looking for devices…"
	kit.Spawn(v.app, adb.Devices, func(ds []adb.Device, err error) {
		if err != nil {
			v.status = "adb: " + err.Error()
			return
		}
		v.SetDevices(ds)
	})
}

// SetDevices replaces the device list, keeping the selection if it is still
// there. Demo devices stay listed.
func (v *View) SetDevices(ds []adb.Device) {
	for serial := range v.demos {
		ds = append(ds, adb.Device{Serial: serial, State: "device", Model: "Demo"})
	}
	v.devices = ds
	v.status = fmt.Sprintf("%d device(s)", len(ds))
	if v.device() == nil && len(ds) > 0 {
		v.selectDevice(ds[0].Serial)
	}
	for _, d := range ds {
		if _, ok := v.info[d.Serial]; ok || d.State != "device" || v.app == nil {
			continue
		}
		serial := d.Serial
		kit.Spawn(v.app, func(ctx context.Context) (adb.Info, error) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return adb.DeviceInfo(ctx, serial)
		}, func(i adb.Info, err error) {
			if err == nil {
				v.info[serial] = i
			}
		})
	}
}

// AddDemo lists a pretend device that streams file (the built-in test
// pattern when ""), to try embedded mirroring without a phone.
func (v *View) AddDemo(file string) {
	serial := fmt.Sprintf("demo-%d", len(v.demos)+1)
	v.demos[serial] = file
	name := "Demo device"
	if file != "" {
		name = filepath.Base(file)
	}
	v.info[serial] = adb.Info{Name: name, Retail: "Pretend device"}
	v.SetDevices(v.realDevices())
}

func (v *View) isDemo(serial string) bool {
	_, ok := v.demos[serial]
	return ok
}

// realDevices is the list without demo devices.
func (v *View) realDevices() []adb.Device {
	var ds []adb.Device
	for _, d := range v.devices {
		if !v.isDemo(d.Serial) {
			ds = append(ds, d)
		}
	}
	return ds
}

// title and subtitle name a device as nicely as what we know allows.
func (v *View) title(d adb.Device) string    { return adb.Title(d, v.info[d.Serial]) }
func (v *View) subtitle(d adb.Device) string { return adb.Subtitle(d, v.info[d.Serial]) }

func (v *View) device() *adb.Device {
	for i := range v.devices {
		if v.devices[i].Serial == v.selected {
			return &v.devices[i]
		}
	}
	return nil
}

func (v *View) selectDevice(serial string) {
	v.selected = serial
	v.appName.SetText(v.options().StartApp)
}

// options returns the selected device's options, creating them.
func (v *View) options() *scrcpy.Options {
	o := v.opts[v.selected]
	if o == nil {
		o = &scrcpy.Options{}
		v.opts[v.selected] = o
	}
	return o
}

// post runs f on the UI goroutine.
// Safe from any goroutine.
func (v *View) post(f func()) {
	if v.app == nil {
		v.postMu.Lock()
		v.posts = append(v.posts, f)
		v.postMu.Unlock()
		return
	}
	v.app.Post(f)
}

// drain runs queued posts (tests; kit.App drains its own).
func (v *View) drain() {
	v.postMu.Lock()
	ps := v.posts
	v.posts = nil
	v.postMu.Unlock()
	for _, f := range ps {
		f()
	}
}

// invalidate requests a frame from any goroutine.
func (v *View) invalidate() {
	if v.app != nil {
		v.app.Invalidate()
	}
}

const (
	tabSettings = iota
	tabScreen
	tabLogs
)

var tabNames = []string{"Settings", "Screen", "Logs"}

// Frame is one frame: handle input, then draw.
func (v *View) Frame(gtx C) {
	v.drain()
	v.handle(gtx)
	v.layout(gtx)
}

func (v *View) handle(gtx C) {
	for _, d := range v.devices {
		c := v.nav.Get(d.Serial)
		v.navGroup.Join(c, d.Serial == v.selected)
		if c.Clicked(gtx) {
			v.selectDevice(d.Serial)
		}
	}
	if v.refresh.Clicked(gtx) {
		v.Refresh()
	}
	if v.add.Clicked(gtx) {
		v.toasts.Push(kit.StatusInfo, "Pairing comes next", "QR and code pairing are not built yet.")
	}
	if v.device() == nil {
		return
	}
	if i, ok := v.tabs.Update(gtx); ok {
		v.tab = i
	}
	if v.start.Clicked(gtx) {
		v.toggleSession()
	}
	switch v.tab {
	case tabLogs:
		v.handleLogs(gtx)
		return
	case tabScreen:
		v.handleScreen(gtx)
		return
	}
	o := v.options()
	if i, ok := v.kind.Update(gtx); ok {
		o.Kind = scrcpy.Kind(i)
	}
	if i, ok := v.kindSel.Update(gtx); ok {
		o.Kind = scrcpy.Kind(i)
	}
	if i, ok := v.fps.Update(gtx); ok {
		o.MaxFPS = fpsVals[i]
	}
	if val, ok := v.bitrate.Update(gtx); ok {
		setBitRate(o, val, v.bitrate.Dragging())
	}
	for _, s := range sections {
		if !s.shown(o.Kind) {
			continue
		}
		for _, it := range s.items {
			if !it.shown(o.Kind) {
				continue
			}
			if it.toggle != nil && v.toggles.Get(it.id).Clicked(gtx) {
				b := it.toggle(o)
				*b = !*b
			}
			if it.choices != nil {
				if i, ok := v.selects.Get(it.id).Update(gtx); ok {
					it.set(o, i)
				}
			}
		}
	}
}

// running reports whether a session (embedded or external) is live.
func (v *View) running(serial string) bool {
	return v.sessions[serial] != nil || v.live[serial] != nil
}

func (v *View) toggleSession() {
	serial := v.selected
	if s := v.sessions[serial]; s != nil {
		s.Stop()
		return
	}
	if ls := v.live[serial]; ls != nil {
		v.stopLive(ls)
		return
	}
	if o := v.options(); o.Embeddable() && !o.SeparateWindow || v.isDemo(serial) {
		v.startLive(serial)
		return
	}
	args := v.options().Args(serial)
	v.log(serial, scrcpy.LogLine{Level: scrcpy.Info, Source: "you", Text: "scrcpy " + strings.Join(args, " ")})
	var parser scrcpy.LogParser // used on the UI goroutine only
	s, err := scrcpy.Start(args, func(line string) {
		v.post(func() { v.log(serial, parser.Parse(line)) })
	})
	if err != nil {
		v.log(serial, scrcpy.LogLine{Level: scrcpy.Error, Source: "scry", Text: err.Error()})
		return
	}
	v.sessions[serial] = s
	go func() {
		err := <-s.Done
		v.post(func() {
			delete(v.sessions, serial)
			if err != nil {
				v.log(serial, scrcpy.LogLine{Level: scrcpy.Error, Source: "scry", Text: "scrcpy exited: " + err.Error()})
			} else {
				v.log(serial, scrcpy.LogLine{Level: scrcpy.Info, Source: "scry", Text: "scrcpy exited"})
			}
		})
	}()
}

func (v *View) layout(gtx C) D {
	th := v.Theme
	return ui.ToastHost{Page: func(gtx C) D {
		return kit.Fill(gtx, th.Bg, func(gtx C) D {
			gtx.Constraints.Min = gtx.Constraints.Max
			return kit.Col(gtx,
				kit.Flexed(1, func(gtx C) D {
					return kit.Row(gtx,
						kit.Rigid(func(gtx C) D {
							// a fifth of the window, within reason
							w := max(180, min(260, gtx.Metric.PxToDp(gtx.Constraints.Max.X)/5))
							return kit.Fixed(w, v.sidebar)(gtx)
						}),
						kit.Rigid(kit.VLine(th.Border)),
						kit.Flexed(1, v.main),
					)
				}),
				kit.Rigid(kit.HLine(th.Border)),
				kit.Rigid(func(gtx C) D {
					return th.Bar(gtx, 4, 16, func(gtx C) D {
						return th.Text(gtx, v.status, th.Type.Small, th.Muted, kit.Regular)
					})
				}),
			)
		})
	}}.Layout(gtx, th, &v.toasts)
}

func (v *View) sidebar(gtx C) D {
	th := v.Theme
	gtx.Constraints.Min = gtx.Constraints.Max
	return kit.Fill(gtx, th.Panel, func(gtx C) D {
		return kit.Flex{Axis: layout.Vertical, Gap: kit.Space2, Pad: kit.Space3}.Layout(gtx, th,
			kit.Rigid(func(gtx C) D {
				return kit.Row(gtx,
					kit.Rigid(func(gtx C) D { return th.Text(gtx, "scry", th.Type.Text, th.Fg, kit.Bold) }),
					kit.Flexed(1, kit.Grow),
					kit.Rigid(func(gtx C) D {
						return ui.Button{ID: "refresh", Text: "Refresh", Style: ui.Ghost, Size: kit.ControlSmall}.Layout(gtx, th, &v.refresh)
					}),
				)
			}),
			kit.Rigid(func(gtx C) D { return th.Caption(gtx, "Devices") }),
			kit.Rigid(func(gtx C) D {
				return v.navGroup.Layout(gtx, "devices", func(gtx C) D {
					children := make([]layout.FlexChild, len(v.devices))
					for i, d := range v.devices {
						children[i] = kit.Rigid(func(gtx C) D {
							trailing, tc := "USB", th.Dim
							if d.Wireless() {
								trailing = "Wi-Fi"
							}
							if v.isDemo(d.Serial) {
								trailing = "Demo"
							}
							if v.running(d.Serial) {
								trailing, tc = "Live", th.Good
							} else if d.State != "device" {
								trailing, tc = d.State, th.Warn
							}
							return ui.NavItem{ID: "device:" + d.Serial, Label: v.title(d), Trailing: trailing, TrailColor: tc, Active: d.Serial == v.selected}.Layout(gtx, th, v.nav.Get(d.Serial))
						})
					}
					return kit.Col(gtx, children...)
				})
			}),
			kit.Flexed(1, kit.Grow),
			kit.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return ui.Button{ID: "add-device", Text: "Add device", Icon: kit.IconPlus}.Layout(gtx, th, &v.add)
			}),
		)
	})
}

func (v *View) main(gtx C) D {
	th := v.Theme
	gtx.Constraints.Min = gtx.Constraints.Max
	d := v.device()
	if d == nil {
		return ui.EmptyState{ID: "no-devices", Title: "No devices", Body: "Plug in a device with USB debugging on, or add one over Wi-Fi."}.Layout(gtx, th)
	}
	if v.tab == tabScreen {
		return kit.Col(gtx,
			kit.Rigid(func(gtx C) D { return v.header(gtx, d) }),
			kit.Rigid(kit.HLine(th.Divider)),
			kit.Flexed(1, v.screenLayout),
		)
	}
	if v.tab == tabLogs {
		return kit.Col(gtx,
			kit.Rigid(func(gtx C) D { return v.header(gtx, d) }),
			kit.Rigid(kit.HLine(th.Divider)),
			kit.Flexed(1, v.logsLayout),
		)
	}
	o := v.options()
	return kit.Col(gtx,
		kit.Rigid(func(gtx C) D { return v.header(gtx, d) }),
		kit.Rigid(func(gtx C) D {
			return layout.Inset{Left: 24, Right: 24, Bottom: 12}.Layout(gtx, func(gtx C) D {
				if gtx.Constraints.Max.X < gtx.Dp(tabsMinW) {
					return kit.Fixed(240, func(gtx C) D {
						return ui.Select{ID: "kind-select", Options: scrcpy.KindNames, Selected: int(o.Kind)}.Layout(gtx, th, &v.kindSel)
					})(gtx)
				}
				return ui.Tabs{ID: "kind", Labels: scrcpy.KindNames, Selected: int(o.Kind), Style: ui.TabsSegmented}.Layout(gtx, th, &v.kind)
			})
		}),
		kit.Rigid(kit.HLine(th.Divider)),
		kit.Flexed(1, func(gtx C) D { return v.settings(gtx, o) }),
		kit.Rigid(kit.HLine(th.Divider)),
		kit.Rigid(func(gtx C) D {
			cmd := "scrcpy " + strings.Join(o.Args(d.Serial), " ")
			return th.Bar(gtx, 10, 24, func(gtx C) D {
				return kit.Col(gtx,
					kit.Rigid(func(gtx C) D { return th.Caption(gtx, "Command") }),
					kit.Rigid(kit.Gap(0, 6)),
					kit.Rigid(func(gtx C) D { return th.CodeBlock(gtx, cmd, th.Type.Small, th.Muted) }),
				)
			})
		}),
	)
}

func (v *View) header(gtx C, d *adb.Device) D {
	th := v.Theme
	label, style := "Start", ui.Solid
	if v.running(d.Serial) {
		label, style = "Stop", ui.Danger
	}
	return layout.Inset{Top: 20, Bottom: 14, Left: 24, Right: 24}.Layout(gtx, func(gtx C) D {
		return kit.Row(gtx,
			kit.Flexed(1, func(gtx C) D {
				return kit.Col(gtx,
					kit.Rigid(func(gtx C) D { return th.Heading(gtx, v.title(*d)) }),
					kit.Rigid(kit.Gap(0, kit.Space1)),
					kit.Rigid(func(gtx C) D { return v.deviceLine(gtx, *d) }),
				)
			}),
			kit.Rigid(func(gtx C) D { return ui.Tabs{ID: "page", Labels: tabNames, Selected: v.tab}.Layout(gtx, th, &v.tabs) }),
			kit.Rigid(kit.Gap(kit.Space4, 0)),
			kit.Rigid(func(gtx C) D {
				return th.Disable(gtx, d.State != "device", func(gtx C) D {
					return ui.Button{ID: "start", Text: label, Style: style}.Layout(gtx, th, &v.start)
				})
			}),
		)
	})
}

// deviceLine is the header's second line: hardware, connection, or what
// the user has to do before the device is usable.
func (v *View) deviceLine(gtx C, d adb.Device) D {
	th := v.Theme
	switch d.State {
	case "device":
	case "unauthorized":
		return th.Text(gtx, "Unauthorized: accept the USB debugging prompt on the device", th.Type.Small, th.Warn, kit.Regular)
	default:
		return th.Text(gtx, "Device is "+d.State, th.Type.Small, th.Warn, kit.Regular)
	}
	conn := "USB " + d.Serial
	switch {
	case v.isDemo(d.Serial):
		conn = "streams a test video over the scrcpy protocol"
	case d.Wireless():
		conn = "Wi-Fi " + d.Serial
	}
	parts := []string{conn}
	if sub := v.subtitle(d); sub != "" {
		parts = append([]string{sub}, parts...)
	}
	return th.Text(gtx, strings.Join(parts, " · "), th.Type.Small, th.Muted, kit.Regular)
}

// Responsive breakpoints for the settings grid and its rows.
const (
	columnMin   = 420 // dp: a column narrower than this is not worth splitting for
	maxColumns  = 3
	stackBelow  = 400 // dp: a card narrower than this stacks labels above controls
	controlMaxW = 280 // dp
	tabsMinW    = 470 // dp: below this the session kind tabs become a dropdown
)

// settings lays out the sections the session kind shows as cards, packed
// into as many columns as fit.
func (v *View) settings(gtx C, o *scrcpy.Options) D {
	th := v.Theme
	var shown []section
	for _, s := range sections {
		if s.shown(o.Kind) {
			shown = append(shown, s)
		}
	}
	return th.List(gtx, &v.scroll, "settings", 1, func(gtx C, _ int) D {
		return layout.Inset{Top: 12, Bottom: 12, Left: 24, Right: 24}.Layout(gtx, func(gtx C) D {
			gap := gtx.Dp(kit.Space3)
			n := max(1, min(maxColumns, len(shown), (gtx.Constraints.Max.X+gap)/(gtx.Dp(columnMin)+gap)))
			cols := pack(shown, o.Kind, n)
			colW := (gtx.Constraints.Max.X - gap*(n-1)) / n
			children := make([]layout.FlexChild, 0, 2*n)
			for i, col := range cols {
				if i > 0 {
					children = append(children, kit.Rigid(kit.Gap(kit.Space3, 0)))
				}
				children = append(children, kit.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X, gtx.Constraints.Max.X = colW, colW
					cards := make([]layout.FlexChild, len(col))
					for j, s := range col {
						cards[j] = kit.Rigid(func(gtx C) D { return v.card(gtx, o, s) })
					}
					return kit.Flex{Axis: layout.Vertical, Gap: kit.Space3}.Layout(gtx, th, cards...)
				}))
			}
			return layout.Flex{Alignment: layout.Start}.Layout(gtx, children...)
		})
	})
}

// pack deals sections into n columns, each to the currently shortest, by
// row count. Order is kept within a column, so reading order holds.
func pack(ss []section, k scrcpy.Kind, n int) [][]section {
	cols := make([][]section, n)
	h := make([]int, n)
	for _, s := range ss {
		rows := 2 // title and padding
		for _, it := range s.items {
			if it.shown(k) {
				rows++
			}
		}
		c := 0
		for i := range h {
			if h[i] < h[c] {
				c = i
			}
		}
		cols[c] = append(cols[c], s)
		h[c] += rows
	}
	return cols
}

func (v *View) card(gtx C, o *scrcpy.Options, s section) D {
	th := v.Theme
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	reason := ""
	if s.disabled != nil {
		reason = s.disabled(o)
	}
	return th.Card(gtx, func(gtx C) D {
		children := []layout.FlexChild{kit.Rigid(func(gtx C) D {
			return kit.Row(gtx,
				kit.Rigid(func(gtx C) D { return th.Caption(gtx, s.title) }),
				kit.Flexed(1, kit.Grow),
				kit.Rigid(func(gtx C) D { return th.Text(gtx, reason, th.Type.Small, th.Dim, kit.Regular) }),
			)
		})}
		for _, it := range s.items {
			if !it.shown(o.Kind) {
				continue
			}
			locked := reason != "" || (o.ReadOnly && o.Kind != scrcpy.OTG && readOnlyLocks(it.id)) ||
				(it.disabled != nil && it.disabled(o))
			children = append(children, kit.Rigid(func(gtx C) D {
				return th.Disable(gtx, locked, func(gtx C) D { return v.item(gtx, o, it) })
			}))
		}
		return kit.Flex{Axis: layout.Vertical, Gap: kit.Space3}.Layout(gtx, th, children...)
	})
}

func (v *View) item(gtx C, o *scrcpy.Options, it item) D {
	th := v.Theme
	if it.toggle != nil {
		on := *it.toggle(o) != it.invert
		return ui.Switch{ID: it.id, Label: it.label, On: on}.Layout(gtx, th, v.toggles.Get(it.id))
	}
	var control kit.W
	switch {
	case it.choices != nil:
		control = func(gtx C) D {
			return ui.Select{ID: it.id, Options: it.choices, Selected: it.get(o)}.Layout(gtx, th, v.selects.Get(it.id))
		}
	case it.custom == "fps":
		control = func(gtx C) D {
			i := 0
			for j, f := range fpsVals {
				if f == o.MaxFPS {
					i = j
				}
			}
			return ui.Tabs{ID: "fps", Labels: fpsNames, Selected: i, Style: ui.TabsSegmented}.Layout(gtx, th, &v.fps)
		}
	case it.custom == "bitrate":
		control = func(gtx C) D { return v.bitrateControl(gtx, o) }
	case it.custom == "app":
		control = func(gtx C) D {
			d := ui.TextField{ID: it.id, Placeholder: "org.mozilla.firefox", Label: it.label}.Layout(gtx, th, &v.appName)
			o.StartApp = strings.TrimSpace(v.appName.Text()) // after the editor took this frame's keys
			return d
		}
	}
	label := func(gtx C) D {
		return kit.Col(gtx,
			kit.Rigid(func(gtx C) D { return th.Text(gtx, it.label, th.Type.Body, th.Fg, kit.Regular) }),
			kit.Rigid(func(gtx C) D {
				if it.hint == "" {
					return D{}
				}
				return th.Text(gtx, it.hint, th.Type.Small, th.Dim, kit.Regular)
			}),
		)
	}
	if gtx.Constraints.Max.X < gtx.Dp(stackBelow) { // narrow card: label above a full-width control
		return kit.Flex{Axis: layout.Vertical, Gap: kit.Space1p5}.Layout(gtx, v.Theme,
			kit.Rigid(label),
			kit.Rigid(kit.Stretch(control)),
		)
	}
	w := min(gtx.Dp(controlMaxW), gtx.Constraints.Max.X/2)
	return kit.Row(gtx,
		kit.Flexed(1, label),
		kit.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
			return control(gtx)
		}),
	)
}

// bitIndex is the bit-rate stop o uses (scrcpy's 8M default when unset).
func bitIndex(o *scrcpy.Options) int {
	for i, r := range bitRates {
		if r == o.BitRate {
			return i
		}
	}
	return 2
}

// setBitRate applies a slider value: the nearest stop, except that a small
// step (a key press: 5%) that would round back to the current stop moves
// to its neighbour, so the keyboard can reach every stop.
func setBitRate(o *scrcpy.Options, v float32, dragging bool) {
	n := len(bitRates) - 1
	cur := bitIndex(o)
	i := int(v*float32(n) + 0.5)
	if d := v - float32(cur)/float32(n); i == cur && !dragging && d != 0 && d > -0.06 && d < 0.06 {
		if d > 0 {
			i++
		} else {
			i--
		}
	}
	r := bitRates[max(0, min(n, i))]
	if r == 8_000_000 {
		r = 0 // scrcpy's default: leave the flag out
	}
	o.BitRate = r
}

// bitrateControl is a slider over fixed stops with the value beside it.
func (v *View) bitrateControl(gtx C, o *scrcpy.Options) D {
	th := v.Theme
	value := float32(bitIndex(o)) / float32(len(bitRates)-1)
	return kit.Row(gtx,
		kit.Flexed(1, func(gtx C) D {
			return ui.Slider{ID: "bitrate", Label: "Bit rate", Value: value}.Layout(gtx, th, &v.bitrate)
		}),
		kit.Rigid(kit.Gap(12, 0)),
		kit.Rigid(kit.Fixed(64, func(gtx C) D {
			return th.Text(gtx, scrcpy.Rate(bitRates[bitIndex(o)])+"bps", th.Type.Body, th.Muted, kit.Medium)
		})),
	)
}
