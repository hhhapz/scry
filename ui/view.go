// Package ui is scry's giokit front end. It edits scrcpy.Options and starts
// sessions; everything it shows comes from the adb and scrcpy packages and
// the schema in settings.go, so it can be replaced wholesale.
package ui

import (
	"fmt"
	"strings"

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
	status   string

	// widget state
	nav      kit.Pool[kit.Clickable]
	navGroup kit.FocusGroup
	refresh  kit.Clickable
	add      kit.Clickable
	start    kit.Clickable
	kind     kit.Tabs
	kindSel  kit.Select // replaces the tabs when they do not fit
	fps      kit.Tabs
	bitrate  kit.Slider
	appName  widget.Editor
	toggles  kit.Pool[kit.Clickable]
	selects  kit.Pool[kit.Select]
	page     kit.List
	toasts   kit.Toasts
}

// New makes the view. With a non-nil app it lists adb devices at once.
func New(app *kit.App) *View {
	th := kit.Dark()
	v := &View{Theme: th, app: app,
		opts:     map[string]*scrcpy.Options{},
		sessions: map[string]*scrcpy.Session{},
	}
	v.kind.Style = kit.TabsSegmented
	v.fps.Style = kit.TabsSegmented
	v.appName.SingleLine = true
	v.page.Axis = layout.Vertical
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

// SetDevices replaces the device list, keeping the selection if it is still there.
func (v *View) SetDevices(ds []adb.Device) {
	v.devices = ds
	v.status = fmt.Sprintf("%d device(s)", len(ds))
	if v.device() == nil && len(ds) > 0 {
		v.selectDevice(ds[0].Serial)
	}
}

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
func (v *View) post(f func()) {
	if v.app == nil {
		f()
		return
	}
	v.app.Post(f)
}

// Frame is one frame: handle input, then draw.
func (v *View) Frame(gtx C) {
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
	o := v.options()
	if i, ok := v.kind.Clicked(gtx); ok {
		o.Kind = scrcpy.Kind(i)
	}
	if i, ok := v.kindSel.Changed(gtx); ok {
		o.Kind = scrcpy.Kind(i)
	}
	if i, ok := v.fps.Clicked(gtx); ok {
		o.MaxFPS = fpsVals[i]
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
				if i, ok := v.selects.Get(it.id).Changed(gtx); ok {
					it.set(o, i)
				}
			}
		}
	}
	if v.start.Clicked(gtx) {
		v.toggleSession()
	}
}

func (v *View) toggleSession() {
	serial := v.selected
	if s := v.sessions[serial]; s != nil {
		s.Stop()
		return
	}
	s, err := scrcpy.Start(v.options().Args(serial), func(line string) {
		v.post(func() { v.status = line })
	})
	if err != nil {
		v.status = err.Error()
		return
	}
	v.sessions[serial] = s
	go func() {
		err := <-s.Done
		v.post(func() {
			delete(v.sessions, serial)
			if err != nil {
				v.status = "scrcpy exited: " + err.Error()
			}
		})
	}()
}

func (v *View) layout(gtx C) D {
	th := v.Theme
	return th.ToastHost(gtx, &v.toasts, func(gtx C) D {
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
	})
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
							if v.sessions[d.Serial] != nil {
								trailing, tc = "Live", th.Good
							} else if d.State != "device" {
								trailing, tc = d.State, th.Warn
							}
							return th.NavItem(gtx, v.nav.Get(d.Serial), "device:"+d.Serial, d.Name(), trailing, tc, d.Serial == v.selected)
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
		return th.EmptyState(gtx, "no-devices", "No devices",
			"Plug in a device with USB debugging on, or add one over Wi-Fi.", nil)
	}
	o := v.options()
	return kit.Col(gtx,
		kit.Rigid(func(gtx C) D { return v.header(gtx, d) }),
		kit.Rigid(func(gtx C) D {
			return layout.Inset{Left: 24, Right: 24, Bottom: 12}.Layout(gtx, func(gtx C) D {
				if gtx.Constraints.Max.X < gtx.Dp(tabsMinW) {
					return kit.Fixed(240, func(gtx C) D {
						return th.Select(gtx, &v.kindSel, "kind-select", scrcpy.KindNames, int(o.Kind))
					})(gtx)
				}
				return th.Tabs(gtx, &v.kind, "kind", scrcpy.KindNames, int(o.Kind))
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
	if v.sessions[d.Serial] != nil {
		label, style = "Stop", ui.Danger
	}
	return layout.Inset{Top: 20, Bottom: 14, Left: 24, Right: 24}.Layout(gtx, func(gtx C) D {
		return kit.Row(gtx,
			kit.Rigid(func(gtx C) D { return th.Heading(gtx, d.Name()) }),
			kit.Rigid(kit.Gap(12, 0)),
			kit.Rigid(func(gtx C) D { return th.Text(gtx, d.Serial, th.Type.Small, th.Dim, kit.Regular) }),
			kit.Flexed(1, kit.Grow),
			kit.Rigid(func(gtx C) D {
				return th.Disable(gtx, d.State != "device", func(gtx C) D {
					return ui.Button{ID: "start", Text: label, Style: style}.Layout(gtx, th, &v.start)
				})
			}),
		)
	})
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
	return th.List(gtx, &v.page, "settings", 1, func(gtx C, _ int) D {
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
			locked := reason != "" || (o.ReadOnly && o.Kind != scrcpy.OTG && readOnlyLocks(it.id))
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
		return th.Switch(gtx, v.toggles.Get(it.id), it.id, it.label, on)
	}
	var control kit.W
	switch {
	case it.choices != nil:
		control = func(gtx C) D {
			return th.Select(gtx, v.selects.Get(it.id), it.id, it.choices, it.get(o))
		}
	case it.custom == "fps":
		control = func(gtx C) D {
			i := 0
			for j, f := range fpsVals {
				if f == o.MaxFPS {
					i = j
				}
			}
			return th.Tabs(gtx, &v.fps, "fps", fpsNames, i)
		}
	case it.custom == "bitrate":
		control = func(gtx C) D { return v.bitrateControl(gtx, o) }
	case it.custom == "app":
		control = func(gtx C) D {
			d := th.TextField(gtx, &v.appName, it.id, "org.mozilla.firefox")
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

// bitrateControl is a slider over fixed stops with the value beside it.
func (v *View) bitrateControl(gtx C, o *scrcpy.Options) D {
	th := v.Theme
	stop := func(f float32) int { return int(f*float32(len(bitRates)-1) + 0.5) }
	cur := 2 // 8M, scrcpy's default
	for i, r := range bitRates {
		if r == o.BitRate {
			cur = i
		}
	}
	if stop(v.bitrate.Value) != cur { // another device, or first frame
		v.bitrate.Value = float32(cur) / float32(len(bitRates)-1)
	}
	return kit.Row(gtx,
		kit.Flexed(1, func(gtx C) D {
			d := th.Slider(gtx, &v.bitrate, "bitrate")
			r := bitRates[stop(v.bitrate.Value)]
			if r == 8_000_000 {
				r = 0 // scrcpy's default: leave the flag out
			}
			o.BitRate = r
			return d
		}),
		kit.Rigid(kit.Gap(12, 0)),
		kit.Rigid(kit.Fixed(64, func(gtx C) D {
			r := o.BitRate
			if r == 0 {
				r = 8_000_000
			}
			return th.Text(gtx, scrcpy.Rate(r)+"bps", th.Type.Body, th.Muted, kit.Medium)
		})),
	)
}
