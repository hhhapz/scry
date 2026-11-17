package scrcpy

import (
	"slices"
	"testing"
)

func TestArgs(t *testing.T) {
	cases := []struct {
		name string
		o    Options
		want []string
	}{
		{"defaults", Options{}, []string{"--serial=x"}},
		{"video", Options{MaxSize: 1920, BitRate: 16_000_000, MaxFPS: 60},
			[]string{"--serial=x", "--max-size=1920", "--video-bit-rate=16M", "--max-fps=60"}},
		{"typing modes", Options{KeyInject: "text"}, []string{"--serial=x", "--prefer-text"}},
		{"typing needs the sdk keyboard", Options{KeyInject: "raw", Keyboard: "uhid"}, []string{"--serial=x", "--keyboard=uhid"}},
		{"read-only drops device options", Options{ReadOnly: true, StayAwake: true, Keyboard: "uhid"},
			[]string{"--serial=x", "--no-control"}},
		{"app window", Options{Kind: AppWindow, NewDisplay: "1920x1080", StartApp: "org.mozilla.firefox"},
			[]string{"--serial=x", "--new-display=1920x1080", "--start-app=org.mozilla.firefox"}},
		{"audio only ignores video and no-audio", Options{Kind: AudioOnly, MaxSize: 1024, NoAudio: true, Fullscreen: true},
			[]string{"--serial=x", "--no-video"}},
		{"otg", Options{Kind: OTG, Keyboard: "aoa", StayAwake: true},
			[]string{"--serial=x", "--otg", "--keyboard=aoa"}},
	}
	for _, c := range cases {
		if got := c.o.Args("x"); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRate(t *testing.T) {
	for in, want := range map[int]string{8_000_000: "8M", 128_000: "128K", 1500: "1500"} {
		if got := Rate(in); got != want {
			t.Errorf("Rate(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLog(t *testing.T) {
	for in, want := range map[string]LogLine{
		"[server] INFO: Device: [samsung] SM-X910": {Info, "server", "Device: [samsung] SM-X910"},
		"WARN: Demuxer 'audio': stream disabled":   {Warn, "client", "Demuxer 'audio': stream disabled"},
		"ERROR: Could not find any ADB device":     {Error, "client", "Could not find any ADB device"},
		"scrcpy-server: 1 file pushed, 0 skipped.": {Info, "adb", "scrcpy-server: 1 file pushed, 0 skipped."},
		"    --display-id=0    (2960x1848)":        {Info, "client", "    --display-id=0    (2960x1848)"},
	} {
		if got := ParseLog(in); got != want {
			t.Errorf("ParseLog(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestLogParserContinuation(t *testing.T) {
	var p LogParser
	p.Parse("[server] INFO: List of displays:")
	if got := p.Parse("    --display-id=0    (2960x1848)"); got.Source != "server" {
		t.Fatalf("continuation source = %q, want server", got.Source)
	}
}
