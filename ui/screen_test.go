package ui

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"

	"github.com/hhhapz/giokit/kittest"

	"github.com/hhhapz/scry/scrcpy/input"
	"github.com/hhhapz/scry/scrcpy/proto"
)

// liveHarness starts the demo device mirrored in scry and waits for video.
// The stream is then paused so frames stay still for strict-mode checks.
func liveHarness(t *testing.T) (*View, *kittest.Harness, *liveSession) {
	t.Helper()
	v := New(nil)
	v.screen.editor.Path = filepath.Join(t.TempDir(), "keymaps.json")
	v.AddDemo("")
	h := kittest.NewT(t, 1100, 900, v.Frame)
	h.Theme = v.Theme
	// Video arrives on its own after Start: frames differing from the
	// click's render is the stream, not a stale frame.
	h.Strict = false
	h.ClickID("start")
	ls := v.live[v.selected]
	if ls == nil || v.tab != tabScreen {
		t.Fatalf("Start did not open an embedded session on the Screen tab (tab %d)", v.tab)
	}
	t.Cleanup(func() {
		if ls.fake != nil {
			ls.fake.Resume()
		}
		if ls.conn != nil {
			ls.conn.Close()
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for ls.frames.Load() < 10 {
		if time.Now().After(deadline) {
			t.Fatal("no video frames from the demo device")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ls.fake.Pause()
	// Let the frames already sent finish decoding.
	for n := int64(-1); n != ls.frames.Load(); {
		n = ls.frames.Load()
		time.Sleep(100 * time.Millisecond)
	}
	h.Settle()
	h.Strict = true
	return v, h, ls
}

// videoPoint is the window point showing frame pixel (x, y).
func videoPoint(t *testing.T, h *kittest.Harness, ls *liveSession, x, y float32) image.Point {
	r, ok := h.Find("screen/video")
	if !ok {
		t.Fatal("video not on screen")
	}
	c := ls.video.Content()
	f := ls.video.Frame()
	return image.Pt(r.Min.X+c.Min.X+int(x*float32(c.Dx())/float32(f.X)), r.Min.Y+c.Min.Y+int(y*float32(c.Dy())/float32(f.Y)))
}

func near(a, b int32) bool { return a-b <= 2 && b-a <= 2 }

func TestEmbeddedVideoShows(t *testing.T) {
	_, h, ls := liveHarness(t)
	if f := ls.video.Frame(); f != image.Pt(360, 640) {
		t.Fatalf("frame %v, want 360x640", f)
	}
	// Video reached the screen: inside the frame differs from the panel
	// around it, in several places.
	bg := h.Pixel(videoPoint(t, h, ls, -20, 320))
	differ := 0
	for _, p := range [][2]float32{{20, 100}, {180, 320}, {300, 500}, {90, 600}} {
		if h.Pixel(videoPoint(t, h, ls, p[0], p[1])) != bg {
			differ++
		}
	}
	if differ < 3 {
		t.Fatalf("only %d of 4 video points differ from the background: the frame is not on screen", differ)
	}
	status := strings.Join(h.TextsIn("screen/video"), " ") + strings.Join(h.Texts(), " ")
	if !strings.Contains(status, "H264") || !strings.Contains(status, "360×640") {
		t.Fatalf("status lacks codec and size: %q", status)
	}
	h.ExpectClean()
	shot(t, h, "screen")
}

func TestClickBecomesTouch(t *testing.T) {
	_, h, ls := liveHarness(t)
	h.Click(videoPoint(t, h, ls, 180, 320))
	got := ls.fake.WaitControls(2, 5*time.Second)
	if len(got) < 2 {
		t.Fatalf("device got %d messages", len(got))
	}
	down, up := got[len(got)-2], got[len(got)-1]
	if down.Type != proto.TypeInjectTouch || down.Action != proto.MotionDown || up.Action != proto.MotionUp {
		t.Fatalf("got %+v then %+v", down, up)
	}
	if p := down.Position; !near(p.X, 180) || !near(p.Y, 320) || p.Width != 360 || p.Height != 640 {
		t.Fatalf("touch at %+v, want about (180,320) in 360x640", p)
	}
	if !ls.focused {
		t.Fatal("clicking the video should give it the keyboard")
	}
}

func TestRightClickIsBack(t *testing.T) {
	_, h, ls := liveHarness(t)
	h.RightClick(videoPoint(t, h, ls, 100, 100))
	var back []proto.Control
	for _, c := range ls.fake.WaitControls(3, 5*time.Second) {
		if c.Type == proto.TypeBackOrScreenOn {
			back = append(back, c)
		}
	}
	if len(back) != 2 || back[0].Action != proto.KeyDown || back[1].Action != proto.KeyUp {
		t.Fatalf("right click sent %+v", back)
	}
}

func TestToolbarKeys(t *testing.T) {
	_, h, ls := liveHarness(t)
	h.ClickID("screen/home")
	got := ls.fake.WaitControls(2, 5*time.Second)
	if len(got) != 2 || got[0].Keycode != proto.KeycodeHome || got[0].Action != proto.KeyDown || got[1].Action != proto.KeyUp {
		t.Fatalf("Home sent %+v", got)
	}
	h.ClickID("screen/power")
	got = ls.fake.WaitControls(3, 5*time.Second)
	if got[2].Type != proto.TypeSetDisplayPower || got[2].On {
		t.Fatalf("Screen off sent %+v", got[2])
	}
	h.ExpectTextIn("screen/power", "Screen on")
}

func TestTypingGoesToDevice(t *testing.T) {
	_, h, ls := liveHarness(t)
	h.Click(videoPoint(t, h, ls, 50, 50)) // focus (a tap)
	before := len(ls.fake.WaitControls(2, 5*time.Second))
	h.Key("A", 0) // a letter: key events (mixed mode)
	h.Type("1!é") // other text: one text message
	got := ls.fake.WaitControls(before+3, 5*time.Second)[before:]
	if len(got) != 3 || got[0].Keycode != proto.KeycodeA || got[0].Action != proto.KeyDown || got[1].Action != proto.KeyUp {
		t.Fatalf("typing sent %+v", got)
	}
	if got[2].Type != proto.TypeInjectText || got[2].Text != "1!é" {
		t.Fatalf("text sent %+v", got[2])
	}
}

func TestGameControls(t *testing.T) {
	v, h, ls := liveHarness(t)
	h.ClickID("screen/game")
	if ls.km.Layout == nil || !v.screen.editor.editing {
		t.Fatal("game controls should start with a starter layout open for editing")
	}
	h.ClickID("keymap/edit")              // Done
	h.Click(videoPoint(t, h, ls, 10, 10)) // focus the video (a tap goes through)
	before := len(ls.fake.WaitControls(2, 5*time.Second))
	h.Key(key.NameSpace, 0) // the starter layout's button at (0.85, 0.75)
	got := ls.fake.WaitControls(before+2, 5*time.Second)[before:]
	if len(got) != 2 || got[0].Action != proto.MotionDown || got[1].Action != proto.MotionUp {
		t.Fatalf("Space sent %+v", got)
	}
	if p := got[0].Position; !near(p.X, 306) || !near(p.Y, 480) {
		t.Fatalf("Space touched %+v, want about (306,480)", p)
	}
	h.ExpectClean()
	shot(t, h, "game")
}

func TestEditLayoutDragAndBind(t *testing.T) {
	v, h, ls := liveHarness(t)
	h.ClickID("screen/game")
	e := &v.screen.editor
	b := ls.km.Layout.Bindings[1] // the Space button
	from := videoPoint(t, h, ls, b.X*360, b.Y*640)
	to := videoPoint(t, h, ls, 180, 160)
	h.Drag(from, to)
	nb := ls.km.Layout.Bindings[1]
	if d := nb.X*360 - 180; d > 3 || d < -3 {
		t.Fatalf("dragged to x %.1f, want 180", nb.X*360)
	}
	if e.selected != 1 {
		t.Fatalf("selection %d after the drag, want 1", e.selected)
	}
	h.Key("E", 0) // rebind the selected button
	if ls.km.Layout.Bindings[1].Key != "E" {
		t.Fatalf("key %q after pressing E", ls.km.Layout.Bindings[1].Key)
	}
	saved, err := input.LoadLayouts(e.Path)
	if err != nil || len(saved.Layouts) != 1 || saved.Layouts[0].Bindings[1].Key != "E" {
		t.Fatalf("saved layouts %+v %v", saved, err)
	}
	h.ClickID("keymap/add-tap")
	h.Key("Q", 0)
	if l := ls.km.Layout.Bindings; len(l) != 3 || l[2].Key != "Q" {
		t.Fatalf("added button %+v", l)
	}
	h.ClickID("keymap/delete")
	if len(ls.km.Layout.Bindings) != 2 {
		t.Fatal("Delete did not remove the selected button")
	}
	h.ExpectClean()
	shot(t, h, "keymap-edit")
}

func TestStopEndsSession(t *testing.T) {
	v, h, ls := liveHarness(t)
	ls.fake.Resume()
	h.Strict = false   // the video is moving again
	h.ClickID("start") // now "Stop"
	deadline := time.Now().Add(5 * time.Second)
	for v.live[v.selected] != nil {
		if time.Now().After(deadline) {
			t.Fatal("session did not end")
		}
		time.Sleep(10 * time.Millisecond)
		h.Settle()
	}
	h.ExpectTextIn("screen-empty", "Not mirroring")
}
