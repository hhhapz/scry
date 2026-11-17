// Package proto is the scrcpy 5.0 wire protocol between the desktop client
// and scrcpy-server on the device: the video/audio stream framing, control
// messages (client to device) and device messages (device to client).
//
// Both directions are implemented, so a fake device (package fake) can
// serve the same bytes a phone would.
//
// Reference: scrcpy v5.0.1, app/src/demuxer.c, app/src/control_msg.c,
// app/src/device_msg.c, server/.../device/Streamer.java and
// DesktopConnection.java.
package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Codec ids as sent in the stream header: the codec name in ASCII.
const (
	CodecH264 uint32 = 0x68323634 // "h264"
	CodecH265 uint32 = 0x68323635 // "h265"
	CodecAV1  uint32 = 0x00617631 // "av1"
	CodecVP8  uint32 = 0x00767038 // "vp8"
	CodecVP9  uint32 = 0x00767039 // "vp9"
	CodecOpus uint32 = 0x6f707573 // "opus"
	CodecAAC  uint32 = 0x00616163 // "aac"
	CodecFLAC uint32 = 0x666c6163 // "flac"
	CodecRaw  uint32 = 0x00726177 // "raw"

	// In place of a codec id: the device disabled the stream.
	StreamDisabled uint32 = 0 // e.g. audio capture unavailable: carry on without it
	StreamError    uint32 = 1 // configuration error: stop
)

// CodecName is the codec id's name ("h264"), or "" if unknown.
func CodecName(id uint32) string {
	switch id {
	case CodecH264:
		return "h264"
	case CodecH265:
		return "h265"
	case CodecAV1:
		return "av1"
	case CodecVP8:
		return "vp8"
	case CodecVP9:
		return "vp9"
	case CodecOpus:
		return "opus"
	case CodecAAC:
		return "aac"
	case CodecFLAC:
		return "flac"
	case CodecRaw:
		return "raw"
	}
	return ""
}

// DeviceNameLen is the size of the device name field sent first.
const DeviceNameLen = 64

const (
	flagSession  = uint64(1) << 63 // a session packet (video only): no payload
	flagConfig   = uint64(1) << 62
	flagKeyFrame = uint64(1) << 61
	ptsMask      = flagKeyFrame - 1

	headerLen = 12
	// MaxPacket bounds a packet's size: anything larger is a broken stream.
	MaxPacket = 64 << 20
)

// ErrStreamDisabled reports that the device turned the stream off
// (StreamDisabled): not fatal for audio.
var ErrStreamDisabled = errors.New("scrcpy: stream disabled by the device")

// ErrStreamConfig reports a configuration error on the device (StreamError).
var ErrStreamConfig = errors.New("scrcpy: stream configuration error on the device")

// ReadDeviceName reads the 64-byte, NUL-padded device name that opens the
// first socket.
func ReadDeviceName(r io.Reader) (string, error) {
	var b [DeviceNameLen]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("read device name: %w", err)
	}
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n]), nil
}

// WriteDeviceName writes the device name field.
func WriteDeviceName(w io.Writer, name string) error {
	var b [DeviceNameLen]byte
	copy(b[:DeviceNameLen-1], name)
	_, err := w.Write(b[:])
	return err
}

// ReadCodec reads a stream's codec id. It returns ErrStreamDisabled or
// ErrStreamConfig when the device sent one of those codes instead.
func ReadCodec(r io.Reader) (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, fmt.Errorf("read codec: %w", err)
	}
	switch id := binary.BigEndian.Uint32(b[:]); id {
	case StreamDisabled:
		return 0, ErrStreamDisabled
	case StreamError:
		return 0, ErrStreamConfig
	default:
		return id, nil
	}
}

// WriteCodec writes a stream's codec id (or StreamDisabled/StreamError).
func WriteCodec(w io.Writer, id uint32) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], id)
	_, err := w.Write(b[:])
	return err
}

// Packet is one media packet.
type Packet struct {
	Data   []byte
	PTS    int64 // microseconds; meaningless for Config packets
	Config bool  // codec configuration, to prepend to the next packet
	Key    bool
}

// Session starts a video session: the size of the frames that follow. The
// device sends one first and again whenever the size changes (rotation,
// a resized virtual display).
type Session struct {
	Width, Height int
	ClientResized bool // the change answers a resize request from the client
}

// Reader reads a stream's packets and session changes.
type Reader struct {
	r   io.Reader
	hdr [headerLen]byte
	buf []byte
}

// NewReader reads packets from r, positioned after the codec id.
func NewReader(r io.Reader) *Reader { return &Reader{r: r} }

// Next returns the next item: a session (s != nil) or a packet. A packet's
// Data is valid until the next call.
func (rd *Reader) Next() (s *Session, p Packet, err error) {
	if _, err = io.ReadFull(rd.r, rd.hdr[:]); err != nil {
		return nil, Packet{}, err
	}
	if rd.hdr[0]&0x80 != 0 {
		return &Session{
			Width:         int(binary.BigEndian.Uint32(rd.hdr[4:8])),
			Height:        int(binary.BigEndian.Uint32(rd.hdr[8:12])),
			ClientResized: rd.hdr[3]&1 != 0,
		}, Packet{}, nil
	}
	flags := binary.BigEndian.Uint64(rd.hdr[0:8])
	n := binary.BigEndian.Uint32(rd.hdr[8:12])
	if n == 0 || n > MaxPacket {
		return nil, Packet{}, fmt.Errorf("scrcpy: bad packet size %d", n)
	}
	if cap(rd.buf) < int(n) {
		rd.buf = make([]byte, n)
	}
	rd.buf = rd.buf[:n]
	if _, err = io.ReadFull(rd.r, rd.buf); err != nil {
		return nil, Packet{}, err
	}
	p = Packet{Data: rd.buf, Config: flags&flagConfig != 0, Key: flags&flagKeyFrame != 0}
	if !p.Config {
		p.PTS = int64(flags & ptsMask)
	}
	return nil, p, nil
}

// WriteSession writes a session packet.
func WriteSession(w io.Writer, s Session) error {
	var b [headerLen]byte
	b[0] = 0x80
	if s.ClientResized {
		b[3] = 1
	}
	binary.BigEndian.PutUint32(b[4:8], uint32(s.Width))
	binary.BigEndian.PutUint32(b[8:12], uint32(s.Height))
	_, err := w.Write(b[:])
	return err
}

// WritePacket writes a media packet with its header.
func WritePacket(w io.Writer, p Packet) error {
	var b [headerLen]byte
	var flags uint64
	if p.Config {
		flags = flagConfig
	} else {
		flags = uint64(p.PTS) & ptsMask
		if p.Key {
			flags |= flagKeyFrame
		}
	}
	binary.BigEndian.PutUint64(b[0:8], flags)
	binary.BigEndian.PutUint32(b[8:12], uint32(len(p.Data)))
	if _, err := w.Write(b[:]); err != nil {
		return err
	}
	_, err := w.Write(p.Data)
	return err
}
