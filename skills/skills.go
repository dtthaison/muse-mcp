// Package skills holds the Claude Code skills that "muse-mcp install" copies
// into place, one directory per skill.
package skills

import "embed"

//go:embed */SKILL.md
var FS embed.FS
