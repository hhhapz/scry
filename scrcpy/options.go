// Package scrcpy describes a scrcpy session as plain data and turns it into
// command-line arguments. It knows nothing about the UI.
package scrcpy

import (
	"fmt"
	"strconv"
)

// Kind is what the session is for; it decides which options apply.
type Kind int

const (
	Mirror    Kind = iota // mirror the device screen
	AppWindow             // an app on a new virtual display
	Camera                // a device camera as the video source
	AudioOnly             // forward audio, no video
	OTG                   // keyboard and mouse only, over USB
)

var KindNames = []string{"Mirror", "App window", "Camera", "Audio only", "Keyboard & mouse"}

// Options is one session's configuration. The zero value is scrcpy's
// defaults: Args emits only what differs.
type Options struct {
	Kind Kind

	// Video
	MaxSize    int    // 0 is native
	BitRate    int    // bits/s, 0 is the default (8M)
	MaxFPS     int    // 0 is unlimited
	VideoCodec string // "" is h264

	// App window
	NewDisplay string // "WxH" or "" for the main display's size
	StartApp   string // package name
	Flex       bool

	// Camera
	CameraFacing string // "", "back", "front", "external"

	// Audio
	NoAudio     bool
	AudioSource string // "" is output

	// Input
	ReadOnly        bool
	Keyboard, Mouse string // "" is sdk; "uhid", "aoa", "disabled"
	NoClipboardSync bool

	// Device
	TurnScreenOff, StayAwake, ShowTouches, PowerOffOnClose bool

	// Window
	Fullscreen, AlwaysOnTop, Borderless bool
	// SeparateWindow launches scrcpy in its own window instead of mirroring
	// inside scry. scry-only: no scrcpy flag.
	SeparateWindow bool

	// Record
	Record string // file path; "" is off
}

// Embeddable reports whether scry can mirror this kind of session in its
// own window (video it can draw). Audio-only and OTG sessions always run
// scrcpy itself.
func (o Options) Embeddable() bool {
	return o.Kind == Mirror || o.Kind == AppWindow || o.Kind == Camera
}

// Args returns the scrcpy arguments for o on the device with this serial.
func (o Options) Args(serial string) []string {
	var a []string
	add := func(s ...string) { a = append(a, s...) }
	flag := func(on bool, f string) {
		if on {
			add(f)
		}
	}
	str := func(v, f string) {
		if v != "" {
			add(f + "=" + v)
		}
	}
	num := func(v int, f string) {
		if v != 0 {
			add(f + "=" + strconv.Itoa(v))
		}
	}
	if serial != "" {
		add("--serial=" + serial)
	}
	switch o.Kind {
	case OTG:
		add("--otg")
		str(o.Keyboard, "--keyboard")
		str(o.Mouse, "--mouse")
		return a
	case AppWindow:
		if o.NewDisplay == "" {
			add("--new-display")
		} else {
			add("--new-display=" + o.NewDisplay)
		}
		str(o.StartApp, "--start-app")
		flag(o.Flex, "--flex-display")
	case Camera:
		add("--video-source=camera")
		str(o.CameraFacing, "--camera-facing")
	case AudioOnly:
		add("--no-video")
	}
	if o.Kind != AudioOnly {
		num(o.MaxSize, "--max-size")
		if o.BitRate != 0 {
			add("--video-bit-rate=" + Rate(o.BitRate))
		}
		num(o.MaxFPS, "--max-fps")
		str(o.VideoCodec, "--video-codec")
	}
	flag(o.NoAudio && o.Kind != AudioOnly, "--no-audio")
	if !o.NoAudio || o.Kind == AudioOnly {
		str(o.AudioSource, "--audio-source")
	}
	if o.Kind != Camera && o.Kind != AudioOnly {
		if o.ReadOnly {
			add("--no-control")
		} else {
			str(o.Keyboard, "--keyboard")
			str(o.Mouse, "--mouse")
			flag(o.NoClipboardSync, "--no-clipboard-autosync")
			flag(o.TurnScreenOff, "--turn-screen-off")
			flag(o.StayAwake, "--stay-awake")
			flag(o.ShowTouches, "--show-touches")
			flag(o.PowerOffOnClose, "--power-off-on-close")
		}
	}
	if o.Kind != AudioOnly {
		flag(o.Fullscreen, "--fullscreen")
		flag(o.AlwaysOnTop, "--always-on-top")
		flag(o.Borderless, "--window-borderless")
	}
	str(o.Record, "--record")
	return a
}

// Rate formats bits/s the way scrcpy accepts them: 8000000 is "8M".
func Rate(bps int) string {
	switch {
	case bps%1_000_000 == 0:
		return fmt.Sprintf("%dM", bps/1_000_000)
	case bps%1000 == 0:
		return fmt.Sprintf("%dK", bps/1000)
	}
	return strconv.Itoa(bps)
}
