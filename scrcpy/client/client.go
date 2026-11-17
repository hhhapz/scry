// Package client runs a scrcpy session itself instead of launching the
// scrcpy program: it starts scrcpy-server on the device, receives the video
// stream and sends input, so the video can be drawn inside scry.
//
// Connect does the adb part (push, tunnel, launch). A Conn is the session
// over any pair of streams, which is how the fake device in package fake
// drives the same code in tests and demos.
package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/hhhapz/scry/scrcpy/proto"
)

// Handler receives a session's events. Each method is called on the
// session's reader goroutines: hand work to the UI goroutine yourself.
type Handler struct {
	// Video is called with the stream's codec ("h264", "h265", "av1"...)
	// once, before any packet.
	Codec func(name string)
	// Session is called when the video size changes, before its packets.
	Session func(s proto.Session)
	// Packet is called for every video packet. p.Data is only valid during
	// the call.
	Packet func(p proto.Packet)
	// Device is called for device messages (clipboard changes, acks).
	Device func(m proto.DeviceMessage)
	// Closed is called once when the session ends, with nil for Close. It
	// may run on the goroutine calling Close.
	Closed func(err error)
	// Stopped is called on the video reader's goroutine after its last
	// Packet call: the place to free a decoder that Packet uses.
	Stopped func()
}

// Conn is a running session.
type Conn struct {
	// DeviceName is the name the device reported, e.g. "SM-X910".
	DeviceName string

	video   io.ReadCloser
	control io.ReadWriteCloser // nil when read-only
	h       Handler

	mu      sync.Mutex // serialises control writes
	buf     []byte
	closing sync.Once
	closed  chan struct{}
	err     error
	cleanup func() // stops the server and the tunnel (Connect)
}

// Open starts a session over video and control streams already connected
// to a server (control may be nil for a read-only session). It reads the
// device name and codec, then reads the stream on its own goroutine.
func Open(video io.ReadCloser, control io.ReadWriteCloser, h Handler) (*Conn, error) {
	c := &Conn{video: video, control: control, h: h, closed: make(chan struct{})}
	name, err := proto.ReadDeviceName(video)
	if err != nil {
		c.closeStreams()
		return nil, err
	}
	c.DeviceName = name
	codec, err := proto.ReadCodec(video)
	if err != nil {
		c.closeStreams()
		return nil, err
	}
	if h.Codec != nil {
		h.Codec(proto.CodecName(codec))
	}
	go c.readVideo()
	if control != nil {
		go c.readDevice()
	}
	return c, nil
}

func (c *Conn) readVideo() {
	if c.h.Stopped != nil {
		defer c.h.Stopped()
	}
	r := proto.NewReader(c.video)
	for {
		s, p, err := r.Next()
		if err != nil {
			c.finish(err)
			return
		}
		if s != nil {
			if c.h.Session != nil {
				c.h.Session(*s)
			}
			continue
		}
		if c.h.Packet != nil {
			c.h.Packet(p)
		}
	}
}

func (c *Conn) readDevice() {
	for {
		m, err := proto.ReadDeviceMessage(c.control)
		if err != nil {
			// The video reader reports the session's end.
			return
		}
		if c.h.Device != nil {
			c.h.Device(m)
		}
	}
}

// ErrReadOnly is returned by Send on a session without control.
var ErrReadOnly = errors.New("scrcpy: session is read-only")

// Send writes a control message. Safe from any goroutine.
func (c *Conn) Send(m proto.Control) error {
	if c.control == nil {
		return ErrReadOnly
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	c.buf, err = m.Encode(c.buf[:0])
	if err != nil {
		return err
	}
	_, err = c.control.Write(c.buf)
	return err
}

// Close ends the session and stops the server.
func (c *Conn) Close() error {
	c.finish(nil)
	return nil
}

// Done is closed when the session has ended; Err then reports why.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// Err is the reason the session ended: nil after Close.
func (c *Conn) Err() error {
	select {
	case <-c.closed:
		return c.err
	default:
		return nil
	}
}

func (c *Conn) finish(err error) {
	// The first caller wins: Close passes nil, and the reader then failing
	// on the closed stream is not reported.
	c.closing.Do(func() {
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
			err = fmt.Errorf("scrcpy: device closed the stream: %w", err)
		}
		c.err = err
		c.closeStreams()
		if c.cleanup != nil {
			c.cleanup()
		}
		close(c.closed)
		if c.h.Closed != nil {
			c.h.Closed(err)
		}
	})
}

func (c *Conn) closeStreams() {
	c.video.Close()
	if c.control != nil {
		c.control.Close()
	}
}
