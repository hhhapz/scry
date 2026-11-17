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

// Wireless reports whether the device is connected over TCP/IP.
func (d Device) Wireless() bool { return strings.Contains(d.Serial, ":") }

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
