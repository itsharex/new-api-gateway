package gateway

import (
	"bytes"
	"encoding/json"
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
// keeping the last usage and model seen. It wraps an io.Writer so it
// can be inserted into the streaming pipeline without extra I/O.
type sseUsageExtractor struct {
	w      io.Writer
	usage  minimalUsage
	model  string
	buf    []byte
	prefix []byte
}

func newSSEUsageExtractor(w io.Writer) *sseUsageExtractor {
	return &sseUsageExtractor{
		w:      w,
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
		e.parsePayload(payload)
	}
	return n, err
}

func (e *sseUsageExtractor) parsePayload(payload []byte) {
	var v struct {
		Usage struct {
			PromptTokens      int `json:"prompt_tokens"`
			CompletionTokens  int `json:"completion_tokens"`
			TotalTokens       int `json:"total_tokens"`
			InputTokens       int `json:"input_tokens"`
			OutputTokens      int `json:"output_tokens"`
			CacheReadTokens   int `json:"cache_read_input_tokens"`
			CacheCreateTokens int `json:"cache_creation_input_tokens"`
			PromptDetails     struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Message struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
		Model string `json:"model"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	u := minimalUsage{
		PromptTokens:     v.Usage.PromptTokens,
		CompletionTokens: v.Usage.CompletionTokens,
		TotalTokens:      v.Usage.TotalTokens,
		ReasoningTokens:  v.Usage.CompletionDetails.ReasoningTokens,
		CachedTokens:     v.Usage.PromptDetails.CachedTokens,
	}
	if u.PromptTokens == 0 && v.Usage.InputTokens > 0 {
		u.PromptTokens = v.Usage.InputTokens
	}
	if u.CompletionTokens == 0 && v.Usage.OutputTokens > 0 {
		u.CompletionTokens = v.Usage.OutputTokens
	}
	// Anthropic message_start: usage nested under message.usage
	if u.PromptTokens == 0 && v.Message.Usage.InputTokens > 0 {
		u.PromptTokens = v.Message.Usage.InputTokens
	}
	if u.CompletionTokens == 0 && v.Message.Usage.OutputTokens > 0 {
		u.CompletionTokens = v.Message.Usage.OutputTokens
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	if u.CachedTokens == 0 {
		u.CachedTokens = v.Usage.CacheReadTokens + v.Usage.CacheCreateTokens
	}
	if u.TotalTokens > 0 {
		e.usage = u
	}
	if v.Model != "" {
		e.model = v.Model
	}
}
