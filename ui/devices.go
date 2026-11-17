package ui

import (
	"context"
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/hhhapz/giokit/kit"
	"github.com/hhhapz/giokit/kit/ui"

	"rsc.io/qr"

	"github.com/hhhapz/scry/adb"
)

// addDevice is the Add device dialog: nearby devices from mDNS, connect by
// address, pair with a code, pair with a QR code.
type addDevice struct {
	dialog kit.Dialog
	tab    int
	tabs   ui.TabsState

	addr, pairAddr, code widget.Editor
	connect, pair, close kit.Clickable

	nearby    []adb.Service
	nearbyBtn kit.Pool[kit.Clickable]
	scanAt    time.Time
	scanning  bool

	busy, err string

	qrCode   *qr.Code
	qrPair   adb.QRPairing
	qrCancel context.CancelFunc
	qrStatus string

	// header actions on the selected device
	disconnect, wifi kit.Clickable
}

var addTabs = []string{"Nearby", "Address", "Pair with code", "QR code"}

const (
	addNearby = iota
	addAddress
	addPairCode
	addQR
)

func (a *addDevice) init() {
	a.addr.SingleLine, a.addr.Submit = true, true
	a.pairAddr.SingleLine = true
	a.code.SingleLine, a.code.Submit = true, true
}

// open shows the dialog on its first tab.
func (a *addDevice) open() {
	a.dialog.Show()
	a.err, a.busy = "", ""
	a.scanAt = time.Time{}
}

func (a *addDevice) stopQR() {
	if a.qrCancel != nil {
		a.qrCancel()
		a.qrCancel = nil
	}
	a.qrCode = nil
}

// trackDevices keeps the device list live with `adb track-devices`,
// restarting it if adb goes away.
func (v *View) trackDevices() {
	go func() {
		for {
			err := adb.Track(context.Background(), func(ds []adb.Device) {
				v.post(func() { v.SetDevices(ds) })
				v.invalidate()
			})
			if err != nil {
				v.post(func() { v.status = "adb: " + err.Error() })
			}
			time.Sleep(3 * time.Second)
		}
	}()
}

// run does an adb action off the UI goroutine, showing it as busy.
func (v *View) adbAction(what string, work func(ctx context.Context) (string, error), done func(string)) {
	a := &v.add
	a.busy, a.err = what, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		res, err := work(ctx)
		v.post(func() {
			a.busy = ""
			if err != nil {
				a.err = err.Error()
				v.toasts.Push(kit.StatusBad, what+" failed", err.Error())
				return
			}
			if done != nil {
				done(res)
			}
			v.Refresh()
		})
		v.invalidate()
	}()
}

func (v *View) handleAddDevice(gtx C) {
	a := &v.add
	if v.addBtn.Clicked(gtx) {
		a.open()
	}
	if a.disconnect.Clicked(gtx) {
		if d := v.device(); d != nil {
			serial := d.Serial
			v.adbAction("Disconnect", func(ctx context.Context) (string, error) {
				return "", adb.Disconnect(ctx, serial)
			}, func(string) { v.toasts.Push(kit.StatusInfo, "Disconnected", v.title(*d)) })
		}
	}
	if a.wifi.Clicked(gtx) {
		if d := v.device(); d != nil {
			serial, name := d.Serial, v.title(*d)
			v.adbAction("Switch to Wi-Fi", func(ctx context.Context) (string, error) {
				ip, err := adb.DeviceIP(ctx, serial)
				if err != nil {
					return "", err
				}
				if err := adb.TCPIP(ctx, serial, 5555); err != nil {
					return "", err
				}
				time.Sleep(time.Second) // adbd restarts in TCP mode
				return adb.Connect(ctx, ip+":5555")
			}, func(string) {
				v.toasts.Push(kit.StatusGood, "On Wi-Fi", name+" can be unplugged now.")
			})
		}
	}
	if !a.dialog.Opened() {
		a.stopQR()
		return
	}
	if a.dialog.DidDismiss() || a.close.Clicked(gtx) {
		a.dialog.Hide()
		a.stopQR()
		return
	}
	if i, ok := a.tabs.Update(gtx); ok {
		a.tab, a.err = i, ""
		if i != addQR {
			a.stopQR()
		}
	}
	submitted := func(e *widget.Editor) bool {
		s := false
		for {
			ev, ok := e.Update(gtx)
			if !ok {
				return s
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				s = true
			}
		}
	}
	connected := func(res string) {
		a.dialog.Hide()
		a.stopQR()
		v.toasts.Push(kit.StatusGood, "Connected", res)
	}
	switch a.tab {
	case addNearby:
		if !a.scanning && gtx.Now.Sub(a.scanAt) > 2*time.Second {
			a.scanning, a.scanAt = true, gtx.Now
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ss, err := adb.MDNSServices(ctx)
				v.post(func() {
					a.scanning = false
					if err == nil {
						a.nearby = ss
					}
				})
				v.invalidate()
			}()
		}
		gtx.Execute(op.InvalidateCmd{At: a.scanAt.Add(2 * time.Second)})
		for _, s := range a.nearby {
			if s.Type == adb.ServiceConnect && a.nearbyBtn.Get(s.Addr).Clicked(gtx) {
				addr := s.Addr
				v.adbAction("Connect", func(ctx context.Context) (string, error) { return adb.Connect(ctx, addr) }, connected)
			}
		}
	case addAddress:
		if (a.connect.Clicked(gtx) || submitted(&a.addr)) && a.busy == "" {
			addr := strings.TrimSpace(a.addr.Text())
			if addr == "" {
				a.err = "Enter the address Wireless debugging shows, like 192.168.0.12:37000."
				break
			}
			v.adbAction("Connect", func(ctx context.Context) (string, error) { return adb.Connect(ctx, addr) }, connected)
		}
	case addPairCode:
		if (a.pair.Clicked(gtx) || submitted(&a.code)) && a.busy == "" {
			addr, code := strings.TrimSpace(a.pairAddr.Text()), strings.TrimSpace(a.code.Text())
			if addr == "" || code == "" {
				a.err = "Enter the IP address & port and the six-digit code from the pairing screen."
				break
			}
			v.adbAction("Pair", func(ctx context.Context) (string, error) {
				if err := adb.Pair(ctx, addr, code); err != nil {
					return "", err
				}
				return "Paired with " + addr + ". It connects by itself in a moment, or use Nearby.", nil
			}, func(res string) {
				a.dialog.Hide()
				v.toasts.Push(kit.StatusGood, "Paired", res)
			})
		}
	case addQR:
		if a.qrCode == nil {
			a.qrPair = adb.NewQRPairing()
			code, err := qr.Encode(a.qrPair.Payload(), qr.M)
			if err != nil {
				a.err = err.Error()
				break
			}
			a.qrCode = code
			a.qrStatus = "Waiting for the phone to scan…"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			a.qrCancel = cancel
			q := a.qrPair
			go func() {
				addr, err := q.Wait(ctx, func(step string) {
					v.post(func() { a.qrStatus = step })
					v.invalidate()
				})
				v.post(func() {
					if ctx.Err() != nil && err != nil {
						return // closed or timed out
					}
					if err != nil {
						a.qrStatus, a.err = "", err.Error()
						return
					}
					connected(addr)
					v.Refresh()
				})
				v.invalidate()
			}()
		}
	}
}

// addDeviceDialog draws the dialog body.
func (v *View) addDeviceDialog(gtx C) D {
	th := v.Theme
	a := &v.add
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(520))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	body := func(gtx C) D {
		return kit.Col(gtx,
			kit.Rigid(func(gtx C) D {
				return ui.Tabs{ID: "add", Labels: addTabs, Selected: a.tab}.Layout(gtx, th, &a.tabs)
			}),
			kit.Rigid(kit.Gap(0, 14)),
			kit.Rigid(func(gtx C) D {
				switch a.tab {
				case addNearby:
					return v.nearbyList(gtx)
				case addAddress:
					return kit.Col(gtx,
						kit.Rigid(func(gtx C) D {
							return th.Paragraph(gtx, "On the device: Settings → Developer options → Wireless debugging. Enter the IP address & port it shows (port 5555 if you ran adb tcpip).", th.Muted)
						}),
						kit.Rigid(kit.Gap(0, 10)),
						kit.Rigid(func(gtx C) D {
							return ui.TextField{ID: "add/address", Placeholder: "192.168.0.12:37000", Label: "Device address"}.Layout(gtx, th, &a.addr)
						}),
					)
				case addPairCode:
					return kit.Col(gtx,
						kit.Rigid(func(gtx C) D {
							return th.Paragraph(gtx, "In Wireless debugging, tap “Pair device with pairing code” and enter what it shows. Pairing is needed once per computer.", th.Muted)
						}),
						kit.Rigid(kit.Gap(0, 10)),
						kit.Rigid(func(gtx C) D {
							return ui.TextField{ID: "add/pair-address", Placeholder: "IP address & port, 192.168.0.12:37199", Label: "Pairing address"}.Layout(gtx, th, &a.pairAddr)
						}),
						kit.Rigid(kit.Gap(0, 8)),
						kit.Rigid(func(gtx C) D {
							return ui.TextField{ID: "add/code", Placeholder: "Pairing code", Label: "Pairing code"}.Layout(gtx, th, &a.code)
						}),
					)
				case addQR:
					return v.qrPanel(gtx)
				}
				return D{}
			}),
			kit.Rigid(func(gtx C) D {
				msg, c := a.busy, th.Muted
				if a.err != "" {
					msg, c = a.err, th.Bad
				}
				if msg == "" {
					return D{}
				}
				return layout.Inset{Top: 10}.Layout(gtx, func(gtx C) D { return th.Paragraph(gtx, msg, c) })
			}),
		)
	}
	actions := []kit.W{func(gtx C) D {
		return ui.Button{ID: "add/close", Text: "Close", Style: ui.Ghost}.Layout(gtx, th, &a.close)
	}}
	switch a.tab {
	case addAddress:
		actions = append(actions, func(gtx C) D {
			return th.Disable(gtx, a.busy != "", func(gtx C) D {
				return ui.Button{ID: "add/connect", Text: "Connect", Style: ui.Solid}.Layout(gtx, th, &a.connect)
			})
		})
	case addPairCode:
		actions = append(actions, func(gtx C) D {
			return th.Disable(gtx, a.busy != "", func(gtx C) D {
				return ui.Button{ID: "add/pair", Text: "Pair", Style: ui.Solid}.Layout(gtx, th, &a.pair)
			})
		})
	}
	return ui.DialogContent{Title: "Add a device", Body: body, Actions: actions}.Layout(gtx, th)
}

// nearbyList shows what mDNS sees: paired devices to connect, devices
// waiting to pair.
func (v *View) nearbyList(gtx C) D {
	th := v.Theme
	a := &v.add
	connected := map[string]bool{}
	for _, d := range v.devices {
		connected[d.Serial] = true
	}
	var rows []layout.FlexChild
	rows = append(rows, kit.Rigid(func(gtx C) D {
		return th.Paragraph(gtx, "Devices with Wireless debugging on, on this network. A device appears here once it has been paired with this computer.", th.Muted)
	}), kit.Rigid(kit.Gap(0, 8)))
	shown := 0
	for _, s := range a.nearby {
		if s.Type != adb.ServiceConnect && s.Type != adb.ServicePairing {
			continue
		}
		shown++
		rows = append(rows, kit.Rigid(func(gtx C) D {
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D {
				return kit.Row(gtx,
					kit.Flexed(1, func(gtx C) D {
						return kit.Col(gtx,
							kit.Rigid(func(gtx C) D { return th.Text(gtx, s.Name, th.Type.Body, th.Fg, kit.Medium) }),
							kit.Rigid(func(gtx C) D { return th.Text(gtx, s.Addr, th.Type.Small, th.Dim, kit.Regular) }),
						)
					}),
					kit.Rigid(func(gtx C) D {
						switch {
						case s.Type == adb.ServicePairing:
							return th.Text(gtx, "Waiting to pair: use a pairing code", th.Type.Small, th.Warn, kit.Regular)
						case connected[s.Addr] || connected[s.Name+"."+adb.ServiceConnect]:
							return th.Text(gtx, "Connected", th.Type.Small, th.Good, kit.Medium)
						}
						return th.Disable(gtx, a.busy != "", func(gtx C) D {
							return ui.Button{ID: "add/nearby/" + s.Addr, Text: "Connect", Style: ui.Primary, Size: kit.ControlSmall}.Layout(gtx, th, a.nearbyBtn.Get(s.Addr))
						})
					}),
				)
			})
		}))
	}
	if shown == 0 {
		rows = append(rows, kit.Rigid(func(gtx C) D {
			return kit.Row(gtx,
				kit.Rigid(func(gtx C) D { return th.Spinner(gtx, 16) }),
				kit.Rigid(kit.Gap(8, 0)),
				kit.Rigid(func(gtx C) D { return th.Text(gtx, "Looking for devices…", th.Type.Body, th.Muted, kit.Regular) }),
			)
		}))
	}
	return kit.Col(gtx, rows...)
}

func (v *View) qrPanel(gtx C) D {
	th := v.Theme
	a := &v.add
	return kit.Row(gtx,
		kit.Rigid(func(gtx C) D { return v.qrImage(gtx, a.qrCode, 200) }),
		kit.Rigid(kit.Gap(16, 0)),
		kit.Flexed(1, func(gtx C) D {
			return kit.Col(gtx,
				kit.Rigid(func(gtx C) D {
					return th.Paragraph(gtx, "In Wireless debugging, tap “Pair device with QR code” and scan this. scry pairs and connects when the phone answers.", th.Muted)
				}),
				kit.Rigid(kit.Gap(0, 10)),
				kit.Rigid(func(gtx C) D { return th.Text(gtx, a.qrStatus, th.Type.Body, th.Fg, kit.Medium) }),
			)
		}),
	)
}

// qrImage draws a QR code, dark on white with a quiet zone, as vectors.
func (v *View) qrImage(gtx C, code *qr.Code, size int) D {
	px := gtx.Dp(unitDp(size))
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	area := clip.Rect{Max: image.Pt(px, px)}.Push(gtx.Ops)
	kit.TagLabel(gtx, "add/qr", "Pairing QR code")
	area.Pop()
	white := color.NRGBA{255, 255, 255, 255}
	black := color.NRGBA{0, 0, 0, 255}
	paint.FillShape(gtx.Ops, white, clip.Rect{Max: image.Pt(px, px)}.Op())
	if code == nil {
		return D{Size: image.Pt(px, px)}
	}
	n := code.Size + 8 // four modules of quiet zone each side
	m := px / n
	off := (px - m*code.Size) / 2
	var p clip.Path
	p.Begin(gtx.Ops)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				r := image.Rect(off+x*m, off+y*m, off+(x+1)*m, off+(y+1)*m)
				p.MoveTo(fpt(r.Min.X, r.Min.Y))
				p.LineTo(fpt(r.Max.X, r.Min.Y))
				p.LineTo(fpt(r.Max.X, r.Max.Y))
				p.LineTo(fpt(r.Min.X, r.Max.Y))
				p.Close()
			}
		}
	}
	paint.FillShape(gtx.Ops, black, clip.Outline{Path: p.End()}.Op())
	return D{Size: image.Pt(px, px)}
}

func unitDp(v int) unit.Dp { return unit.Dp(v) }

func fpt(x, y int) f32.Point { return f32.Pt(float32(x), float32(y)) }
