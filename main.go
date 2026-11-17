// Command scry is a desktop front end for scrcpy.
package main

import (
	"github.com/hhhapz/giokit/kit"

	"github.com/hhhapz/scry/ui"
)

func main() {
	app := kit.NewApp("scry", "scry", 1100, 760)
	app.Run(ui.New(app).Frame)
}
