package forms

import "strings"

const navySGR = "\x1b[38;2;233;245;255;48;2;9;38;111m"
const modalSGR = "\x1b[38;2;233;245;255;48;2;12;49;133m"
const goldFG = "\x1b[38;2;255;223;134m"

func navyCanvas(content string) string {
	restore := strings.NewReplacer("\x1b[m", navySGR, "\x1b[0m", navySGR, "\x1b[49m", navySGR)
	return navySGR + restore.Replace(content) + "\x1b[m"
}

func modalCanvas(content string) string {
	restore := strings.NewReplacer("\x1b[m", modalSGR, "\x1b[0m", modalSGR, "\x1b[49m", modalSGR)
	return modalSGR + restore.Replace(content) + "\x1b[m"
}
