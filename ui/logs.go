package ui

import (
	"context"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/hhhapz/giokit/kit"
	"github.com/hhhapz/giokit/kit/ui"

	"github.com/hhhapz/scry/adb"
	"github.com/hhhapz/scry/scrcpy"
)

// logsPage is the Logs tab: everything scrcpy and adb printed for a device,
// filterable, plus a box to run adb commands against it.
type logsPage struct {
	bufs    map[string]*kit.LogBuffer // per device serial
	view    kit.LogView
	filter  widget.Editor
	level   ui.SelectState
	clear   kit.Clickable
	console widget.Editor
	run     kit.Clickable
}

var (
	levelNames = []string{"All", "Info", "Warnings", "Errors"}
	levelVals  = []kit.LogLevel{kit.LogDebug, kit.LogInfo, kit.LogWarn, kit.LogError}
	toKit      = map[scrcpy.Level]kit.LogLevel{scrcpy.Debug: kit.LogDebug, scrcpy.Info: kit.LogInfo, scrcpy.Warn: kit.LogWarn, scrcpy.Error: kit.LogError}
)

func (p *logsPage) init() {
	p.bufs = map[string]*kit.LogBuffer{}
	p.filter.SingleLine = true
	p.console.SingleLine = true
	p.console.Submit = true
}

func (p *logsPage) buf(serial string) *kit.LogBuffer {
	b := p.bufs[serial]
	if b == nil {
		b = &kit.LogBuffer{}
		p.bufs[serial] = b
	}
	return b
}

// log appends one line for a device. Call it on the UI goroutine.
func (v *View) log(serial string, l scrcpy.LogLine) {
	v.logs.buf(serial).Append(kit.LogLine{Time: time.Now(), Level: toKit[l.Level], Source: l.Source, Text: l.Text})
	if l.Level >= scrcpy.Warn || l.Source != "adb" {
		v.status = l.Text
	}
}

func (v *View) handleLogs(gtx C) {
	p := &v.logs
	if i, ok := p.level.Update(gtx); ok {
		p.view.MinLevel = levelVals[i]
	}
	if p.clear.Clicked(gtx) {
		p.buf(v.selected).Clear()
	}
	submit := p.run.Clicked(gtx)
	for {
		e, ok := p.console.Update(gtx)
		if !ok {
			break
		}
		if _, ok := e.(widget.SubmitEvent); ok {
			submit = true
		}
	}
	if args := strings.Fields(p.console.Text()); submit && len(args) > 0 {
		p.console.SetText("")
		v.runAdb(v.selected, args)
	}
}

// runAdb runs an adb command for the device and logs it and its output.
func (v *View) runAdb(serial string, args []string) {
	v.log(serial, scrcpy.LogLine{Level: scrcpy.Info, Source: "you", Text: "adb -s " + serial + " " + strings.Join(args, " ")})
	kit.Spawn(v.app, func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return adb.Run(ctx, serial, args...)
	}, func(out string, err error) {
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if line != "" {
				v.log(serial, scrcpy.LogLine{Level: scrcpy.Info, Source: "adb", Text: line})
			}
		}
		if err != nil {
			v.log(serial, scrcpy.LogLine{Level: scrcpy.Error, Source: "adb", Text: err.Error()})
		}
	})
}

func (v *View) logsLayout(gtx C) D {
	th := v.Theme
	p := &v.logs
	sel := 0
	for i, l := range levelVals {
		if l == p.view.MinLevel {
			sel = i
		}
	}
	return kit.Col(gtx,
		kit.Rigid(func(gtx C) D {
			return layout.Inset{Top: 12, Bottom: 12, Left: 24, Right: 24}.Layout(gtx, func(gtx C) D {
				return kit.Row(gtx,
					kit.Flexed(1, func(gtx C) D {
						d := ui.TextField{ID: "log-filter", Placeholder: "Filter logs", Icon: kit.IconSearch}.Layout(gtx, th, &p.filter)
						p.view.Filter = p.filter.Text() // after the editor took this frame's keys
						return d
					}),
					kit.Rigid(kit.Gap(kit.Space2, 0)),
					kit.Rigid(kit.Fixed(140, func(gtx C) D {
						return ui.Select{ID: "log-level", Options: levelNames, Selected: sel}.Layout(gtx, th, &p.level)
					})),
					kit.Rigid(kit.Gap(kit.Space2, 0)),
					kit.Rigid(func(gtx C) D {
						return ui.Button{ID: "log-clear", Text: "Clear", Style: ui.Ghost}.Layout(gtx, th, &p.clear)
					}),
				)
			})
		}),
		kit.Rigid(kit.HLine(th.Divider)),
		kit.Flexed(1, func(gtx C) D {
			b := p.buf(v.selected)
			if b.Len() == 0 {
				gtx.Constraints.Min = gtx.Constraints.Max
				return ui.EmptyState{ID: "logs-empty", Title: "No logs yet", Body: "Start a session or run an adb command below."}.Layout(gtx, th)
			}
			return layout.Inset{Left: 16, Right: 8}.Layout(gtx, func(gtx C) D {
				return th.LogView(gtx, &p.view, b, "logs")
			})
		}),
		kit.Rigid(kit.HLine(th.Divider)),
		kit.Rigid(func(gtx C) D {
			return th.Bar(gtx, 10, 24, func(gtx C) D {
				return kit.Row(gtx,
					kit.Rigid(func(gtx C) D {
						return th.Code(gtx, "adb -s "+v.selected, th.Type.Small, th.Muted)
					}),
					kit.Rigid(kit.Gap(kit.Space2, 0)),
					kit.Flexed(1, func(gtx C) D {
						return ui.TextField{ID: "adb-command", Placeholder: "shell getprop ro.build.version.release", Label: "adb command"}.Layout(gtx, th, &p.console)
					}),
					kit.Rigid(kit.Gap(kit.Space2, 0)),
					kit.Rigid(func(gtx C) D {
						return ui.Button{ID: "adb-run", Text: "Run"}.Layout(gtx, th, &p.run)
					}),
				)
			})
		}),
	)
}
