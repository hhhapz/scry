// Package fake is a pretend Android device: it serves a video file over the
// scrcpy protocol, exactly as scrcpy-server would, and records the input it
// receives. scry uses it for tests and for its demo device, so the whole
// embedded-mirroring path runs without a phone.
package fake

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hhhapz/giokit/media"

	"github.com/hhhapz/scry/scrcpy/client"
	"github.com/hhhapz/scry/scrcpy/proto"
)

// PhoneClip is a 3-second 360×640 H.264 test pattern, portrait like a
// phone screen.
//
//go:embed testdata/phone.mp4
var PhoneClip []byte

// Device serves one session.
type Device struct {
	Name string // reported device name; default "Demo device"
	File string // video file to stream; default PhoneClip
	Loop bool   // restart the file at its end (else the session ends)
	// Realtime paces packets by their timestamps; off, they go out as fast
	// as the reader takes them (tests).
	Realtime bool

	mu        sync.Mutex
	controls  []proto.Control
	clipboard string
	notify    chan struct{}
}

// Controls returns the control messages received so far.
func (d *Device) Controls() []proto.Control {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]proto.Control(nil), d.controls...)
}

// WaitControls blocks until n control messages arrived or the timeout
// passes, and returns them.
func (d *Device) WaitControls(n int, timeout time.Duration) []proto.Control {
	deadline := time.After(timeout)
	for {
		d.mu.Lock()
		if len(d.controls) >= n {
			c := append([]proto.Control(nil), d.controls...)
			d.mu.Unlock()
			return c
		}
		if d.notify == nil {
			d.notify = make(chan struct{}, 1)
		}
		ch := d.notify
		d.mu.Unlock()
		select {
		case <-ch:
		case <-deadline:
			return d.Controls()
		}
	}
}

// Connect starts the device on in-memory pipes and opens a client session
// on them: what client.Connect does with a real phone.
func (d *Device) Connect(h client.Handler) (*client.Conn, error) {
	vc, vs := net.Pipe()
	cc, cs := net.Pipe()
	go func() {
		err := d.Serve(vs, cs)
		vs.Close()
		cs.Close()
		_ = err
	}()
	return client.Open(vc, cc, h)
}

// path returns the file to stream, writing PhoneClip out once if needed.
func (d *Device) path() (string, error) {
	if d.File != "" {
		return d.File, nil
	}
	p := filepath.Join(os.TempDir(), fmt.Sprintf("scry-phone-%d.mp4", len(PhoneClip)))
	if st, err := os.Stat(p); err == nil && st.Size() == int64(len(PhoneClip)) {
		return p, nil
	}
	if err := os.WriteFile(p, PhoneClip, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// Serve speaks the server side of the protocol on video and control until
// either closes (or, without Loop, the file ends).
func (d *Device) Serve(video io.WriteCloser, control io.ReadWriteCloser) error {
	path, err := d.path()
	if err != nil {
		return err
	}
	f, err := media.OpenFile(path)
	if err != nil {
		return err
	}
	name := d.Name
	if name == "" {
		name = "Demo device"
	}
	go d.readControls(control)

	var hdr bytes.Buffer
	proto.WriteDeviceName(&hdr, name)
	proto.WriteCodec(&hdr, codecID(f.Codec))
	proto.WriteSession(&hdr, proto.Session{Width: f.Width, Height: f.Height})
	if _, err := video.Write(hdr.Bytes()); err != nil {
		f.Close()
		return err
	}
	var offset int64 // added to PTS so a looped file keeps counting up
	start := time.Now()
	for {
		if c := f.Config(); c != nil {
			if err := proto.WritePacket(video, proto.Packet{Data: c, Config: true}); err != nil {
				f.Close()
				return err
			}
		}
		var last int64
		for {
			p, err := f.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				f.Close()
				return err
			}
			pts := offset + p.PTS
			last = p.PTS
			if d.Realtime {
				if wait := time.Until(start.Add(time.Duration(pts) * time.Microsecond)); wait > 0 {
					time.Sleep(wait)
				}
			}
			if err := proto.WritePacket(video, proto.Packet{Data: p.Data, PTS: pts, Key: p.Key}); err != nil {
				f.Close()
				return err
			}
		}
		f.Close()
		if !d.Loop {
			return nil
		}
		frame := int64(1e6 / max(f.FPS, 1))
		offset += last + frame
		if f, err = media.OpenFile(path); err != nil {
			return err
		}
	}
}

func (d *Device) readControls(control io.ReadWriteCloser) {
	for {
		c, err := proto.ReadControl(control)
		if err != nil {
			return
		}
		d.mu.Lock()
		d.controls = append(d.controls, c)
		var reply *proto.DeviceMessage
		switch c.Type {
		case proto.TypeSetClipboard:
			d.clipboard = c.Text
			if c.Sequence != 0 {
				reply = &proto.DeviceMessage{Type: proto.DeviceAckClipboard, Sequence: c.Sequence}
			}
		case proto.TypeGetClipboard:
			reply = &proto.DeviceMessage{Type: proto.DeviceClipboard, Text: d.clipboard}
		}
		if d.notify != nil {
			select {
			case d.notify <- struct{}{}:
			default:
			}
		}
		d.mu.Unlock()
		if reply != nil {
			proto.WriteDeviceMessage(control, *reply)
		}
	}
}

// SetClipboard changes the device clipboard and tells the client, as a
// phone does when the user copies something.
func (d *Device) SetClipboard(control io.Writer, text string) error {
	d.mu.Lock()
	d.clipboard = text
	d.mu.Unlock()
	return proto.WriteDeviceMessage(control, proto.DeviceMessage{Type: proto.DeviceClipboard, Text: text})
}

func codecID(c media.Codec) uint32 {
	switch c {
	case media.H264:
		return proto.CodecH264
	case media.H265:
		return proto.CodecH265
	case media.AV1:
		return proto.CodecAV1
	case media.VP8:
		return proto.CodecVP8
	case media.VP9:
		return proto.CodecVP9
	}
	return proto.StreamError
}
