//go:build !unix

package render

import "os"

// TermSize is a fixed 80x24 off unix (no Windows console support yet).
func TermSize(*os.File) (cols, rows int) { return 80, 24 }
