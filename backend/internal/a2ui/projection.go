package a2ui

import (
	"os"
	"strings"
)

// EnvProjection is the emergency fail-closed write switch (design §12).
// When set to "off" (case-insensitive), completeRun skips a2ui attach/persist
// and only writes fence-stripped plain text.
const EnvProjection = "AAP_A2UI_PROJECTION"

// ProjectionEnabled reports whether A2UI write-path attach is allowed.
// Fail-closed: only the exact value "off" disables; unset/other → enabled.
func ProjectionEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv(EnvProjection)), "off")
}
