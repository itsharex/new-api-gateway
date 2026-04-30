package gateway

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

type MultipartPartEvidence struct {
	Name        string
	Filename    string
	ContentType string
	SizeBytes   int64
	Data        []byte
}

func isMultipart(req *http.Request) bool {
	if req == nil {
		return false
	}

	contentType := req.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "multipart/") {
		return true
	}
	if err != nil {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/")
	}
	return false
}

func captureMultipartParts(traceID, contentType string, body []byte) ([]MultipartPartEvidence, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return nil, nil
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, multipart.ErrMessageTooLarge
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := []MultipartPartEvidence{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			return nil, err
		}
		contentType := part.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		parts = append(parts, MultipartPartEvidence{
			Name:        part.FormName(),
			Filename:    part.FileName(),
			ContentType: contentType,
			SizeBytes:   int64(len(data)),
			Data:        data,
		})
	}
	return parts, nil
}
