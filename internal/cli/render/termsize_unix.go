//go:build unix

package render

import (
	"os"
	"syscall"
	"unsafe"
)

// TermSize is f's terminal size in columns and rows, 80x24 when f is not a terminal or the ioctl
// fails. It is read on every redraw, so a resize is picked up without a SIGWINCH handler.
// ponytail: stdlib ioctl, unix only (no Windows console); switch to golang.org/x/term if that matters.
func TermSize(f *os.File) (cols, rows int) {
	var ws struct{ Row, Col, X, Y uint16 }
	//nolint:gosec // TIOCGWINSZ has no non-unsafe stdlib path; &ws is a valid, live local, never escaped
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.Col == 0 || ws.Row == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}
