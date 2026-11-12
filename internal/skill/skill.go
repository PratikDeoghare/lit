// Package skill holds the Claude skill that teaches the notation; it is embedded in the binary.
package skill

import "embed"

//go:embed all:files
var FS embed.FS

// Root is the directory inside FS that holds the skill's files.
const Root = "files"
