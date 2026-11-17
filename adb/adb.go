// Package adb lists devices by shelling out to adb.
package adb

import (
	"context"
	"os/exec"
	"strings"
)

// Device is one line of `adb devices -l`.
type Device struct {
	Serial string // "192.168.0.241:43629" or a USB serial
	State  string // "device", "unauthorized", "offline", ...
	Model  string // "SM_X910"
}

// Wireless reports whether the device is connected over TCP/IP: an
// address, or an mDNS name adb connected to by itself.
func (d Device) Wireless() bool {
	return strings.Contains(d.Serial, ":") || strings.Contains(d.Serial, "._adb-tls-connect.")
}

// Name is what to show: the model with underscores as spaces, or the serial.
func (d Device) Name() string {
	if d.Model == "" {
		return d.Serial
	}
	return strings.ReplaceAll(d.Model, "_", " ")
}

// Devices runs `adb devices -l`.
func Devices(ctx context.Context) ([]Device, error) {
	out, err := exec.CommandContext(ctx, "adb", "devices", "-l").Output()
	if err != nil {
		return nil, err
	}
	return ParseDevices(string(out)), nil
}

// ParseDevices parses `adb devices -l` output.
func ParseDevices(out string) []Device {
	var ds []Device
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(line, "List of") || strings.HasPrefix(line, "*") {
			continue
		}
		d := Device{Serial: f[0], State: f[1]}
		for _, kv := range f[2:] {
			if m, ok := strings.CutPrefix(kv, "model:"); ok {
				d.Model = m
			}
		}
		ds = append(ds, d)
	}
	return ds
}

// Run runs `adb -s serial args...` and returns its combined output.
func Run(ctx context.Context, serial string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "adb", append([]string{"-s", serial}, args...)...).CombinedOutput()
	return string(out), err
}

// Info is what a connected, authorized device says about itself.
type Info struct {
	Name         string // the user's name for it (Settings → About), "mira"
	Retail       string // the marketing name, "Galaxy Tab S9 Ultra"
	Manufacturer string // "samsung"
	Model        string // "SM-X910"
	Android      string // "16"
	SoC          string // "SM8550"
}

// infoScript prints key=value lines. Only these keys are read: the settings
// tables also hold account details we have no business collecting.
const infoScript = `echo name=$(settings get global device_name)
echo retail=$(settings get global default_device_name)
echo market=$(getprop ro.product.marketname)
echo manufacturer=$(getprop ro.product.manufacturer)
echo model=$(getprop ro.product.model)
echo android=$(getprop ro.build.version.release)
echo soc=$(getprop ro.soc.model)`

// DeviceInfo asks the device for its names and versions, in one round trip.
func DeviceInfo(ctx context.Context, serial string) (Info, error) {
	out, err := Run(ctx, serial, "shell", infoScript)
	if err != nil {
		return Info{}, err
	}
	return ParseInfo(out), nil
}

// ParseInfo parses infoScript's output.
func ParseInfo(out string) Info {
	var i Info
	market := ""
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || v == "null" {
			continue
		}
		switch k {
		case "name":
			i.Name = v
		case "retail":
			i.Retail = v
		case "market":
			market = v
		case "manufacturer":
			i.Manufacturer = v
		case "model":
			i.Model = v
		case "android":
			i.Android = v
		case "soc":
			i.SoC = v
		}
	}
	if i.Retail == "" {
		i.Retail = market
	}
	return i
}

// Title is the best name to show for d, given what its Info says (zero if unknown).
func Title(d Device, i Info) string {
	switch {
	case i.Name != "":
		return i.Name
	case i.Retail != "":
		return i.Retail
	case i.Model != "":
		return upper(i.Manufacturer) + " " + i.Model
	}
	return d.Name()
}

// Subtitle describes the hardware: "Galaxy Tab S9 Ultra · Android 16", leaving
// out whatever the title already says.
func Subtitle(d Device, i Info) string {
	var parts []string
	if t := Title(d, i); i.Retail != "" && i.Retail != t {
		parts = append(parts, i.Retail)
	} else if i.Retail == "" && i.Model != "" && !strings.Contains(t, i.Model) {
		parts = append(parts, upper(i.Manufacturer)+" "+i.Model)
	}
	if i.Android != "" {
		parts = append(parts, "Android "+i.Android)
	}
	return strings.Join(parts, " · ")
}

func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
