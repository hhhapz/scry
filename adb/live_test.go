//go:build live

package adb

import (
	"context"
	"testing"
)

// go test -tags live ./adb -run Live -v  (asks the first adb device)
func TestLiveInfo(t *testing.T) {
	ds, err := Devices(context.Background())
	if err != nil || len(ds) == 0 {
		t.Skip("no device")
	}
	i, err := DeviceInfo(context.Background(), ds[0].Serial)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v\ntitle %q, subtitle %q", i, Title(ds[0], i), Subtitle(ds[0], i))
}
