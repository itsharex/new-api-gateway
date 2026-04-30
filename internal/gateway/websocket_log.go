package gateway

import (
	"bytes"
	"regexp"
	"sync"
)

var (
	webSocketTokenFragment        = `(?:[A-Za-z0-9._~+/=-]+|\r?\n(?:client|upstream)\s+)+`
	realtimeKeyPattern            = regexp.MustCompile(`(?i)(openai-insecure-api-key\.)` + webSocketTokenFragment)
	webSocketAuthorizationPattern = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)` + webSocketTokenFragment)
	bareAPIKeyPattern             = regexp.MustCompile(`(?i)\bsk-` + webSocketTokenFragment)
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
	line := direction + " " + string(data) + "\n"
	remaining := l.limit - l.buf.Len()
	if len(line) > remaining {
		line = line[:remaining]
	}
	_, _ = l.buf.WriteString(line)
}

func (l *boundedWebSocketLog) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	text := redactWebSocketLog(l.buf.String())
	if len(text) > l.limit {
		text = text[:l.limit]
	}
	return []byte(text)
}

func (l *boundedWebSocketLog) String() string {
	return string(l.Bytes())
}

func redactWebSocketLog(value string) string {
	value = realtimeKeyPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = webSocketAuthorizationPattern.ReplaceAllString(value, "${1}[REDACTED]")
	return bareAPIKeyPattern.ReplaceAllString(value, "[REDACTED]")
}
