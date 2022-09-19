package build

import "strings"

var invalidFilenameChars = []string{
	"<",
	">",
	":",
	" ",
	"/",
	"\\",
	"|",
	"?",
	"*",
}

func fixFilename(name string) string {
	for _, i := range invalidFilenameChars {
		name = strings.ReplaceAll(name, i, "_")
	}

	return name
}
