package adb

import (
	"slices"
	"testing"
)

func TestParseDevices(t *testing.T) {
	out := `List of devices attached
192.168.0.241:43629    device product:gts9uwifixx model:SM_X910 device:gts9uwifi transport_id:2
R5CT1234ABC            unauthorized usb:1-4 transport_id:3

`
	want := []Device{
		{Serial: "192.168.0.241:43629", State: "device", Model: "SM_X910"},
		{Serial: "R5CT1234ABC", State: "unauthorized"},
	}
	if got := ParseDevices(out); !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}
