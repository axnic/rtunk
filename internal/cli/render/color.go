package render

// SGR codes used by the human renderer.
const (
	sgrBold   = "1"
	sgrDim    = "2"
	sgrRed    = "31"
	sgrGreen  = "32"
	sgrYellow = "33"
)

// paint wraps s in the SGR code when color is on, and returns it unchanged otherwise.
func paint(color bool, code, s string) string {
	if !color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
