// Command eloqi is a cross-platform desktop voice input tool. It runs as a
// daemon, listening for a global hotkey to capture speech and transcribe it
// to the clipboard or directly into the focused window.
package main

import (
	"os"

	"github.com/xiangchang24/eloqi/internal/app"
)

// appVersion is replaced by release builds through -ldflags. Keeping a useful
// development value makes locally built binaries self-describing.
var appVersion = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		_, _ = os.Stdout.WriteString("eloqi " + appVersion + "\n")
		return
	}
	os.Exit(app.Run())
}
