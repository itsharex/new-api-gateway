package ids

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

func NewTraceID() string {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		panic("ids: failed to read random trace id bytes: " + err.Error())
	}
	return "trace_" + hex.EncodeToString(buf)
}
