// Nitro UberPad - a multiplatform, scriptable text editor for files of any size.
// https://www.nitrobutton.com  -  nfn@nitrobutton.com
package main

import (
	"os"

	"nitrobutton.com/nitro-uber-pad/internal/ui"
)

func main() {
	ui.Run(os.Args[1:])
}
