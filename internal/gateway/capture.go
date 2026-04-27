package gateway

import (
	"bytes"
	"io"
	"net/http"
)

type CapturedRequest struct {
	BodyBytes   []byte
	ContentType string
	SizeBytes   int64
}

func captureRequestBody(req *http.Request) (CapturedRequest, error) {
	if req.Body == nil {
		return CapturedRequest{}, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return CapturedRequest{}, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return CapturedRequest{BodyBytes: body, ContentType: req.Header.Get("Content-Type"), SizeBytes: int64(len(body))}, nil
}
