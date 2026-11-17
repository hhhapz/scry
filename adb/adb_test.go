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

func TestInfo(t *testing.T) {
	out := "name=mira\nretail=Galaxy Tab S9 Ultra\nmarket=\nmanufacturer=samsung\nmodel=SM-X910\nandroid=16\nsoc=SM8550\n"
	d := Device{Serial: "192.168.0.241:43629", State: "device", Model: "SM_X910"}
	i := ParseInfo(out)
	if got := Title(d, i); got != "mira" {
		t.Errorf("Title = %q", got)
	}
	if got := Subtitle(d, i); got != "Galaxy Tab S9 Ultra · Android 16" {
		t.Errorf("Subtitle = %q", got)
	}

	// no user name, marketname instead of Samsung's key, "null" from settings
	i = ParseInfo("name=null\nretail=null\nmarket=Redmi Note 13\nmanufacturer=Xiaomi\nmodel=23129RAA4G\nandroid=14\n")
	if got, sub := Title(d, i), Subtitle(d, i); got != "Redmi Note 13" || sub != "Android 14" {
		t.Errorf("Title, Subtitle = %q, %q", got, sub)
	}

	// unauthorized: nothing but the listing
	if got, sub := Title(d, Info{}), Subtitle(d, Info{}); got != "SM X910" || sub != "" {
		t.Errorf("Title, Subtitle = %q, %q", got, sub)
	}
}
