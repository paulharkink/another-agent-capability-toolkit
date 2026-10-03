package forms

import "strings"

const navySGR = "\x1b[38;2;233;245;255;48;2;9;38;111m"

func navyCanvas(content string) string {
	restore := strings.NewReplacer("\x1b[m", navySGR, "\x1b[0m", navySGR, "\x1b[49m", navySGR)
	return navySGR + restore.Replace(content) + "\x1b[m"
}
