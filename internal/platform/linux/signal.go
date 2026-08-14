//go:build linux

package linux

import "syscall"

// sigInterrupt is the signal sent to arecord to make it flush and exit.
var sigInterrupt = syscall.SIGINT
