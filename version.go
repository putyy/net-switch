package netswitch

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionText string

func Version() string {
	return strings.TrimSpace(versionText)
}
