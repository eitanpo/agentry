//go:build darwin

package parse

import (
	"os"
	"syscall"
	"time"
)

// fileBirthtime returns the file's creation time, which the darwin kernel records
// as st_birthtime. This is the signal that orders a fork family: a fork is a new
// file, so its birthtime exceeds the original's, and unlike the in-conversation
// timestamps a fork copies verbatim, the file's birthtime cannot be forged.
func fileBirthtime(fi os.FileInfo) (time.Time, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	bt := st.Birthtimespec
	return time.Unix(bt.Sec, bt.Nsec), true
}
