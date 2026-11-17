package ui

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/op/paint"

	"github.com/hhhapz/giokit/kittest"
)

// The cost of the whole window per video frame: the embedded view redraws
// everything when a frame lands (docs/design/video.md, "Risks").
//
//	go test -run x -bench Screen ./ui
func benchScreen(b *testing.B, newFrame bool) {
	v := New(nil)
	v.screen.editor.Path = filepath.Join(b.TempDir(), "keymaps.json")
	v.AddDemo("")
	h, err := kittest.New(1600, 1000, 1, v.Frame)
	if err != nil {
		b.Skip(err)
	}
	defer h.Release()
	h.Theme = v.Theme
	h.Strict = false
	h.ClickID("start")
	ls := v.live[v.selected]
	deadline := time.Now().Add(10 * time.Second)
	for ls.frames.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ls.fake.Pause()
	time.Sleep(200 * time.Millisecond)
	// Two tablet-sized frames to alternate, as the decoder would submit.
	var fs [2]*image.RGBA
	for k := range fs {
		fs[k] = image.NewRGBA(image.Rect(0, 0, 1848, 2960))
		for i := range fs[k].Pix {
			fs[k].Pix[i] = byte(i + k)
		}
	}
	h.Settle()
	b.ResetTimer()
	t0 := time.Now()
	for i := 0; i < b.N; i++ {
		if newFrame {
			ls.stream.Submit(paint.StreamFrame{Image: fs[i%2]})
		}
		h.Settle()
	}
	_ = h.Pixel(image.Pt(1, 1))
	b.ReportMetric(float64(time.Since(t0).Microseconds())/float64(b.N)/1000, "ms/frame")
	ls.fake.Resume()
	ls.conn.Close()
}

func BenchmarkScreenNewFrame(b *testing.B) { benchScreen(b, true) }
func BenchmarkScreenRedraw(b *testing.B)   { benchScreen(b, false) }
