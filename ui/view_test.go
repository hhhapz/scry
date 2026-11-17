package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hhhapz/giokit/kittest"

	"github.com/hhhapz/scry/adb"
	"github.com/hhhapz/scry/scrcpy"
)

var fixture = []adb.Device{
	{Serial: "192.168.0.241:43629", State: "device", Model: "SM_X910"},
	{Serial: "R5CT1234ABC", State: "unauthorized"},
}

func harness(t *testing.T) (*View, *kittest.Harness) {
	v := New(nil)
	v.SetDevices(fixture)
	h := kittest.NewT(t, 1100, 1300, v.Frame) // tall: every section on screen
	h.Theme = v.Theme
	return v, h
}

func TestSettingsEditOptions(t *testing.T) {
	v, h := harness(t)
	h.ClickID("stay-awake")
	h.ClickID("read-only")
	o := v.opts[fixture[0].Serial]
	if !o.StayAwake || !o.ReadOnly {
		t.Fatalf("options not updated: %+v", o)
	}
	h.ExpectTextIn("settings", "Read-only mode is on")
	h.ClickID("kind/App window")
	if o.Kind != scrcpy.AppWindow {
		t.Fatalf("kind = %v", o.Kind)
	}
	h.ExpectTextIn("settings", "Launch app")
	h.ExpectClean()
	shot(t, h, "app-window")
}

func TestKindsHideSections(t *testing.T) {
	_, h := harness(t)
	for _, k := range scrcpy.KindNames {
		h.ClickID("kind/" + k)
		h.ExpectClean()
		shot(t, h, k)
	}
}

func TestSwitchDevice(t *testing.T) {
	v, h := harness(t)
	h.ClickID("fullscreen")
	h.ClickID("device:" + fixture[1].Serial)
	if v.selected != fixture[1].Serial || v.options().Fullscreen {
		t.Fatal("options should be per device")
	}
	h.ExpectClean()
	shot(t, h, "unauthorized")
}

// shot saves the frame when SCRY_SHOTS is set.
func shot(t *testing.T, h *kittest.Harness, name string) {
	dir := os.Getenv("SCRY_SHOTS")
	if dir == "" {
		return
	}
	if err := h.Save(filepath.Join(dir, name+".png")); err != nil {
		t.Fatal(err)
	}
}

// TestResponsive checks the settings grid's column count at common window
// sizes, by whether Video's and Audio's controls share a column.
func TestResponsive(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
		cols int
	}{
		{"narrow", 720, 900, 1},
		{"laptop", 1280, 800, 2},
		{"16x9", 1920, 1080, 3},
	} {
		v := New(nil)
		v.SetDevices(fixture)
		h := kittest.NewT(t, c.w, c.h, v.Frame)
		h.Theme = v.Theme
		shot(t, h, "size-"+c.name)
		xs := map[int]bool{}
		for _, id := range []string{"max-size/field", "audio-source/field", "keyboard/field"} {
			r, ok := h.Find(id)
			if !ok {
				t.Fatalf("%s: %s not on screen", c.name, id)
			}
			xs[r.Min.X] = true
		}
		if len(xs) != c.cols {
			t.Errorf("%s: %d columns, want %d", c.name, len(xs), c.cols)
		}
		h.ExpectClean()
	}
}

func TestNarrowKindDropdown(t *testing.T) {
	v := New(nil)
	v.SetDevices(fixture)
	h := kittest.NewT(t, 640, 800, v.Frame)
	h.Theme = v.Theme
	if _, ok := h.Find("kind/Mirror"); ok {
		t.Fatal("tabs should give way to the dropdown when narrow")
	}
	h.ClickID("kind-select/field")
	h.ClickID("kind-select/3")
	if v.options().Kind != scrcpy.AudioOnly {
		t.Fatalf("kind = %v", v.options().Kind)
	}
	h.ExpectClean()
	shot(t, h, "size-phone")
}

func TestLogs(t *testing.T) {
	v, h := harness(t)
	serial := fixture[0].Serial
	for _, f := range []string{"cameras", "encoders"} {
		data, err := os.ReadFile(filepath.Join("..", "docs", "samples", f+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		var p scrcpy.LogParser
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			v.log(serial, p.Parse(line))
		}
	}
	v.log(serial, scrcpy.LogLine{Level: scrcpy.Warn, Source: "client", Text: "Demuxer 'audio': stream explicitly disabled by the device"})
	h.ClickID("page/Logs")
	h.ExpectTextIn("logs", "List of video encoders:")
	h.ClickID("log-level/field")
	h.ClickID("log-level/2") // warnings and up
	if n := v.logs.view.Count(v.logs.buf(serial)); n != 1 {
		t.Fatalf("warnings: %d lines, want 1", n)
	}
	h.ClickID("log-level/field")
	h.ClickID("log-level/0")
	h.ClickID("log-filter")
	h.Type("hevc")
	if n := v.logs.view.Count(v.logs.buf(serial)); n != 6 {
		t.Fatalf("filter hevc: %d lines, want 6", n)
	}
	h.ExpectClean()
	shot(t, h, "logs")
}
