package gateway

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

func teeStream(src io.Reader, client io.Writer, capture io.Writer) (int64, error) {
	return io.Copy(io.MultiWriter(client, capture), src)
}

func copyStreamToClientAndCapture(src io.Reader, client io.Writer, capture io.Writer) (int64, error, error) {
	buf := make([]byte, 32*1024)
	var written int64
	var captureErr error
	for {
		nr, readErr := src.Read(buf)
		if nr > 0 {
			chunk := buf[:nr]
			nw, writeErr := client.Write(chunk)
			if nw > 0 {
				written += int64(nw)
			}
			if writeErr != nil {
				return written, writeErr, captureErr
			}
			if nw != nr {
				return written, io.ErrShortWrite, captureErr
			}
			if capture != nil {
				captured, err := capture.Write(chunk)
				if err != nil {
					captureErr = err
					capture = nil
				} else if captured != nr {
					captureErr = io.ErrShortWrite
					capture = nil
				}
			}
		}
		if readErr == io.EOF {
			return written, nil, captureErr
		}
		if readErr != nil {
			return written, readErr, captureErr
		}
	}
}

func isStreamingResponse(resp *http.Response) bool {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	return strings.Contains(contentType, "text/event-stream")
}

type flushWriter struct {
	writer  io.Writer
	flusher http.Flusher
}

func (w flushWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if w.flusher != nil {
		w.flusher.Flush()
	}
	return n, err
}

// sseUsageExtractor parses SSE "data:" lines as they pass through,
// delegating payload parsing to a format-specific usageExtractor.
// It wraps an io.Writer so it can be inserted into the streaming
// pipeline without extra I/O.
type sseUsageExtractor struct {
	w      io.Writer
	ext    usageExtractor
	buf    []byte
	prefix []byte
}

func newSSEUsageExtractor(w io.Writer, ext usageExtractor) *sseUsageExtractor {
	return &sseUsageExtractor{
		w:      w,
		ext:    ext,
		prefix: []byte("data: "),
	}
}

func (e *sseUsageExtractor) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if err != nil {
		return n, err
	}
	e.buf = append(e.buf, p...)
	for {
		idx := bytes.IndexByte(e.buf, '\n')
		if idx < 0 {
			break
		}
		line := e.buf[:idx]
		e.buf = e.buf[idx+1:]
		line = bytes.TrimRight(line, "\r")
		if !bytes.HasPrefix(line, e.prefix) {
			continue
		}
		payload := bytes.TrimLeft(line[len(e.prefix):], " ")
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		e.ext.processSSE(payload)
	}
	return n, err
}

func (e *sseUsageExtractor) result() (minimalUsage, string) {
	return e.ext.sseResult()
}

func (e *sseUsageExtractor) assembledResult() []byte {
	return e.ext.assembleSSE()
}
