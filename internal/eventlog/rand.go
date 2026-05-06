package eventlog

import (
	"crypto/rand"
	"io"
)

// randReader is a tiny indirection so tests can stub it deterministically
// (we don't actually stub today, but the seam exists when needed).
func randReader() io.Reader { return rand.Reader }
