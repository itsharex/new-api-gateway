package ids

import (
	"crypto/rand"
	"encoding/hex"
)

func NewTraceID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return "trace_" + hex.EncodeToString(buf)
}
