package ui

import (
	"slices"

	"github.com/hhhapz/scry/scrcpy"
)

// The settings schema: which controls exist, where they live and which
// Options field each edits. The layout code only walks this table, so
// reshaping the UI mostly means editing data here.

type section struct {
	title    string
	kinds    []scrcpy.Kind                  // shown for these kinds
	disabled func(o *scrcpy.Options) string // non-empty: the reason it is disabled
	items    []item
}

func (s section) shown(k scrcpy.Kind) bool { return slices.Contains(s.kinds, k) }

// item is one control: a toggle, a choice, or a custom widget by name.
type item struct {
	id, label, hint string
	kinds           []scrcpy.Kind // nil: every kind the section shows

	toggle func(o *scrcpy.Options) *bool
	invert bool // the switch shows "on" when the flag is false

	choices []string                    // labels
	get     func(o *scrcpy.Options) int // index of the current choice
	set     func(o *scrcpy.Options, i int)

	custom string // "bitrate", "fps", "app"

	// disabled, if set, locks the item when it returns true.
	disabled func(o *scrcpy.Options) bool
}

func (it item) shown(k scrcpy.Kind) bool { return it.kinds == nil || slices.Contains(it.kinds, k) }

func toggle(id, label string, f func(o *scrcpy.Options) *bool) item {
	return item{id: id, label: label, toggle: f}
}

// strChoice binds a choice to a string field; values[i] is labels[i]'s value.
func strChoice(id, label string, f func(o *scrcpy.Options) *string, labels, values []string) item {
	return item{id: id, label: label, choices: labels,
		get: func(o *scrcpy.Options) int { return max(0, slices.Index(values, *f(o))) },
		set: func(o *scrcpy.Options, i int) { *f(o) = values[i] },
	}
}

func intChoice(id, label string, f func(o *scrcpy.Options) *int, labels []string, values []int) item {
	return item{id: id, label: label, choices: labels,
		get: func(o *scrcpy.Options) int { return max(0, slices.Index(values, *f(o))) },
		set: func(o *scrcpy.Options, i int) { *f(o) = values[i] },
	}
}

var (
	all     = []scrcpy.Kind{scrcpy.Mirror, scrcpy.AppWindow, scrcpy.Camera, scrcpy.AudioOnly, scrcpy.OTG}
	visual  = []scrcpy.Kind{scrcpy.Mirror, scrcpy.AppWindow, scrcpy.Camera}
	control = []scrcpy.Kind{scrcpy.Mirror, scrcpy.AppWindow}

	inputModes  = []string{"Standard", "Physical (UHID)", "USB (AOA)", "Off"}
	inputValues = []string{"", "uhid", "aoa", "disabled"}

	bitRates = []int{2_000_000, 4_000_000, 8_000_000, 16_000_000, 32_000_000, 64_000_000}
	fpsNames = []string{"30", "60", "90", "120", "Max"}
	fpsVals  = []int{30, 60, 90, 120, 0}
)

func readOnly(o *scrcpy.Options) string {
	if o.ReadOnly {
		return "Read-only mode is on"
	}
	return ""
}

var sections = []section{
	{title: "App window", kinds: []scrcpy.Kind{scrcpy.AppWindow}, items: []item{
		strChoice("vd-size", "Display size", func(o *scrcpy.Options) *string { return &o.NewDisplay },
			[]string{"Match device", "1920 × 1080", "2560 × 1440", "1280 × 800"},
			[]string{"", "1920x1080", "2560x1440", "1280x800"}),
		{id: "app", label: "Launch app", hint: "Package name, e.g. org.mozilla.firefox", custom: "app"},
		toggle("flex", "Resize display with window", func(o *scrcpy.Options) *bool { return &o.Flex }),
	}},
	{title: "Camera", kinds: []scrcpy.Kind{scrcpy.Camera}, items: []item{
		strChoice("camera", "Camera", func(o *scrcpy.Options) *string { return &o.CameraFacing },
			[]string{"Default", "Back", "Front", "External"}, []string{"", "back", "front", "external"}),
	}},
	{title: "Video", kinds: visual, items: []item{
		intChoice("max-size", "Resolution", func(o *scrcpy.Options) *int { return &o.MaxSize },
			[]string{"Native", "2560", "1920", "1600", "1280", "1024", "720"},
			[]int{0, 2560, 1920, 1600, 1280, 1024, 720}),
		{id: "bitrate", label: "Bit rate", custom: "bitrate"},
		{id: "fps", label: "Frame rate", custom: "fps"},
		strChoice("codec", "Codec", func(o *scrcpy.Options) *string { return &o.VideoCodec },
			[]string{"H.264", "H.265", "AV1"}, []string{"", "h265", "av1"}),
	}},
	{title: "Audio", kinds: []scrcpy.Kind{scrcpy.Mirror, scrcpy.AppWindow, scrcpy.Camera, scrcpy.AudioOnly}, items: []item{
		{id: "audio", label: "Forward audio", kinds: visual, invert: true,
			toggle: func(o *scrcpy.Options) *bool { return &o.NoAudio }},
		strChoice("audio-source", "Source", func(o *scrcpy.Options) *string { return &o.AudioSource },
			[]string{"Device sound", "App playback", "Microphone", "Microphone (calls)"},
			[]string{"", "playback", "mic", "mic-voice-communication"}),
	}},
	{title: "Input", kinds: []scrcpy.Kind{scrcpy.Mirror, scrcpy.AppWindow, scrcpy.OTG}, items: []item{
		{id: "read-only", label: "Read-only (view, don't control)", kinds: control,
			toggle: func(o *scrcpy.Options) *bool { return &o.ReadOnly }},
		strChoice("keyboard", "Keyboard", func(o *scrcpy.Options) *string { return &o.Keyboard }, inputModes, inputValues),
		strChoice("mouse", "Mouse", func(o *scrcpy.Options) *string { return &o.Mouse }, inputModes, inputValues),
		{id: "clipboard", label: "Sync clipboard", kinds: control, invert: true,
			toggle: func(o *scrcpy.Options) *bool { return &o.NoClipboardSync }},
	}},
	{title: "Device", kinds: control, disabled: readOnly, items: []item{
		toggle("screen-off", "Turn device screen off", func(o *scrcpy.Options) *bool { return &o.TurnScreenOff }),
		toggle("stay-awake", "Stay awake while plugged in", func(o *scrcpy.Options) *bool { return &o.StayAwake }),
		toggle("show-touches", "Show physical touches", func(o *scrcpy.Options) *bool { return &o.ShowTouches }),
		toggle("power-off", "Turn screen off when closing", func(o *scrcpy.Options) *bool { return &o.PowerOffOnClose }),
	}},
	{title: "Window", kinds: visual, items: []item{
		toggle("separate", "Open in a separate scrcpy window", func(o *scrcpy.Options) *bool { return &o.SeparateWindow }),
		scrcpyWindow(toggle("fullscreen", "Start fullscreen", func(o *scrcpy.Options) *bool { return &o.Fullscreen })),
		scrcpyWindow(toggle("on-top", "Always on top", func(o *scrcpy.Options) *bool { return &o.AlwaysOnTop })),
		scrcpyWindow(toggle("borderless", "Borderless", func(o *scrcpy.Options) *bool { return &o.Borderless })),
	}},
}

// scrcpyWindow marks an item that only affects scrcpy's own window.
func scrcpyWindow(it item) item {
	it.disabled = func(o *scrcpy.Options) bool { return !o.SeparateWindow }
	return it
}

// readOnlyLocks lists the Input items that read-only disables.
func readOnlyLocks(id string) bool { return id == "keyboard" || id == "mouse" || id == "clipboard" }
