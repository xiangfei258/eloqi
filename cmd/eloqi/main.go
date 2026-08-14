// Command eloqi is a cross-platform desktop voice input tool. It runs as a
// daemon, listening for a global hotkey to capture speech and transcribe it
// to the clipboard or directly into the focused window.
package main

import (
	"os"

	"github.com/xiangchang24/eloqi/internal/app"
)

// appVersion is the current build version of Eloqi.
const appVersion = "0.1.0"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		os.Stdout.WriteString("eloqi " + appVersion + "\n")
		return
	}
	os.Exit(app.Run())
}
