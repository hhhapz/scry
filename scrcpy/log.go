package scrcpy

import "strings"

// Level is a log line's severity.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

// LogLine is one parsed line of scrcpy output.
type LogLine struct {
	Level  Level
	Source string // "client", "server" (the part running on the device) or "adb"
	Text   string
}

var levels = map[string]Level{"VERBOSE": Debug, "DEBUG": Debug, "INFO": Info, "WARN": Warn, "ERROR": Error}

// ParseLog splits a line like "[server] INFO: Device: ..." into its parts.
// Lines without a level (list output, adb's push progress) are Info.
func ParseLog(s string) LogLine {
	l := LogLine{Level: Info, Source: "client", Text: s}
	if rest, ok := strings.CutPrefix(s, "[server] "); ok {
		l.Source, s, l.Text = "server", rest, rest
	}
	if tag, rest, ok := strings.Cut(s, ": "); ok {
		if lv, ok := levels[tag]; ok {
			l.Level, l.Text = lv, rest
			return l
		}
	}
	if strings.Contains(s, "file pushed") || strings.HasPrefix(s, "adb: ") {
		l.Source = "adb"
	}
	return l
}

// LogParser parses a stream of lines: an indented line with no level of its
// own continues the previous message (multi-line lists from the server).
type LogParser struct{ last LogLine }

func (p *LogParser) Parse(s string) LogLine {
	l := ParseLog(s)
	if l.Text == s && strings.HasPrefix(s, " ") && p.last.Source != "" {
		l.Level, l.Source = p.last.Level, p.last.Source
	}
	p.last = l
	return l
}
