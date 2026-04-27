package gateway

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"
)

func TestDetectMultipart(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "audio.wav")
	_, _ = part.Write([]byte("abc"))
	_ = writer.Close()

	req, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if !isMultipart(req) {
		t.Fatal("expected multipart")
	}
}
