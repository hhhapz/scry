package scrcpy

import (
	"bufio"
	"os/exec"
)

// Session is a running scrcpy process.
type Session struct {
	cmd  *exec.Cmd
	Done chan error // receives once, when the process exits
}

// Start launches scrcpy with args. Each stderr/stdout line goes to logf,
// on scrcpy's goroutine: hand it to the UI with app.Post.
func Start(args []string, logf func(string)) (*Session, error) {
	cmd := exec.Command("scrcpy", args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &Session{cmd: cmd, Done: make(chan error, 1)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			logf(sc.Text())
		}
		s.Done <- cmd.Wait()
	}()
	return s, nil
}

// Stop ends the session; Done reports when it has exited.
func (s *Session) Stop() { s.cmd.Process.Kill() }
