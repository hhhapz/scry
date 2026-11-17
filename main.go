// Command scry is a desktop front end for scrcpy: it manages adb devices,
// mirrors them inside its own window (or launches scrcpy), and maps keys to
// touches for games.
//
//	scry                  # your adb devices
//	scry -demo            # plus a pretend device streaming a test pattern
//	scry -demo-file x.mp4 # plus a pretend device streaming x.mp4
package main

import (
	"flag"

	"github.com/hhhapz/giokit/kit"

	"github.com/hhhapz/scry/ui"
)

func main() {
	demo := flag.Bool("demo", false, "add a demo device that streams a test pattern")
	demoFile := flag.String("demo-file", "", "add a demo device that streams this video file")
	flag.Parse()
	app := kit.NewApp("scry", "scry", 1100, 760)
	v := ui.New(app)
	if *demo {
		v.AddDemo("")
	}
	if *demoFile != "" {
		v.AddDemo(*demoFile)
	}
	app.Run(v.Frame)
}
