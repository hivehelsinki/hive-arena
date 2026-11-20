// Package docs embeds the game rules, so that the ladder website can show them
package docs

import _ "embed"

//go:embed rules.md
var Rules string
