package gateway

import (
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
