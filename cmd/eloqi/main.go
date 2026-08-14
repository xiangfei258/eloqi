package main

import (
	"fmt"
	"os"
)

// appVersion is the current build version of Eloqi.
const appVersion = "0.1.0"

func main() {
	fmt.Fprintf(os.Stdout, "Eloqi %s - 桌面语音输入工具（开发中）\n", appVersion)
}
