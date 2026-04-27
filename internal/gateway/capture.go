package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

const DefaultMaxRequestBodyBytes int64 = 32 << 20

var ErrRequestBodyTooLarge = errors.New("request body exceeds configured limit")

type CapturedRequest struct {
	BodyBytes   []byte
	ContentType string
	SizeBytes   int64
}

func captureRequestBody(req *http.Request, maxBytes int64) (CapturedRequest, error) {
	if req.Body == nil {
		return CapturedRequest{}, nil
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxRequestBodyBytes
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBytes+1))
	if err != nil {
		return CapturedRequest{}, err
	}
	if int64(len(body)) > maxBytes {
		return CapturedRequest{}, ErrRequestBodyTooLarge
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return CapturedRequest{BodyBytes: body, ContentType: req.Header.Get("Content-Type"), SizeBytes: int64(len(body))}, nil
}
