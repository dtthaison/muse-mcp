// Package skills holds the Claude Code skills that "muse-mcp install" copies
// into place, one directory per skill with SKILL.md and its reference files.
package skills

import "embed"

//go:embed */*.md
var FS embed.FS

// Retired are skills older versions installed; install removes them.
var Retired = []string{"muse-voice"}
