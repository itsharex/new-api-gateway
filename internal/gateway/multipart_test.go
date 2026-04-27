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

func TestDetectMultipartMalformedParametersUsesMediaType(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	req.Header.Set("Content-Type", " Multipart/Form-Data; boundary")
	if !isMultipart(req) {
		t.Fatal("expected multipart")
	}
}

func TestDetectMultipartRawFallbackTrimsAndLowercases(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	req.Header.Set("Content-Type", " Multipart/Form-Data@bad")
	if !isMultipart(req) {
		t.Fatal("expected multipart")
	}
}

func TestDetectMultipartMixedCase(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	req.Header.Set("Content-Type", "Multipart/Form-Data; Boundary=abc")
	if !isMultipart(req) {
		t.Fatal("expected multipart")
	}
}

func TestDetectMultipartNonMultipart(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Content-Type", "application/json")
	if isMultipart(req) {
		t.Fatal("expected non-multipart")
	}
}

func TestDetectMultipartMissingContentType(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/responses", nil)
	if isMultipart(req) {
		t.Fatal("expected non-multipart")
	}
}

func TestDetectMultipartNilRequest(t *testing.T) {
	if isMultipart(nil) {
		t.Fatal("expected non-multipart")
	}
}
