//go:build !darwin

package parse

import (
	"os"
	"time"
)

// fileBirthtime has no portable implementation off darwin — Linux exposes a
// creation time only via statx, and not every filesystem carries one — so callers
// fall back to the modification time. Reported as unavailable here.
func fileBirthtime(os.FileInfo) (time.Time, bool) {
	return time.Time{}, false
}
