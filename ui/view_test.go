package ui

import (
	"os"
	"path/filepath"
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
