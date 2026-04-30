package gateway

import (
	"bytes"
	"regexp"
	"sync"
)

var (
	realtimeKeyPattern            = regexp.MustCompile(`openai-insecure-api-key\.[A-Za-z0-9._~+/=-]+`)
	webSocketAuthorizationPattern = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[A-Za-z0-9._~+/=-]+`)
)

type boundedWebSocketLog struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func newBoundedWebSocketLog(limit int) *boundedWebSocketLog {
	if limit <= 0 {
		limit = 1 << 20
	}
	return &boundedWebSocketLog{limit: limit}
}

func (l *boundedWebSocketLog) WriteClient(data []byte) (int, error) {
	l.write("client", data)
	return len(data), nil
}

func (l *boundedWebSocketLog) WriteUpstream(data []byte) (int, error) {
	l.write("upstream", data)
	return len(data), nil
}

func (l *boundedWebSocketLog) write(direction string, data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buf.Len() >= l.limit {
		return
	}
	line := direction + " " + redactWebSocketLog(string(data)) + "\n"
	remaining := l.limit - l.buf.Len()
	if len(line) > remaining {
		line = line[:remaining]
	}
	_, _ = l.buf.WriteString(line)
}

func (l *boundedWebSocketLog) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf.Bytes()...)
}

func (l *boundedWebSocketLog) String() string {
	return string(l.Bytes())
}

func redactWebSocketLog(value string) string {
	value = realtimeKeyPattern.ReplaceAllString(value, "openai-insecure-api-key.[REDACTED]")
	return webSocketAuthorizationPattern.ReplaceAllString(value, "${1}[REDACTED]")
}
