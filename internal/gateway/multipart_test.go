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

func TestCaptureMultipartPartsExtractsFieldsAndFiles(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("prompt", "make a diagram"); err != nil {
		t.Fatalf("WriteField error = %v", err)
	}
	part, err := writer.CreateFormFile("image", "input.png")
	if err != nil {
		t.Fatalf("CreateFormFile error = %v", err)
	}
	_, _ = part.Write([]byte("png-bytes"))
	if err := writer.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	parts, err := captureMultipartParts("trace_1", writer.FormDataContentType(), body.Bytes())
	if err != nil {
		t.Fatalf("captureMultipartParts error = %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %#v", parts)
	}
	if parts[0].Name != "prompt" || parts[0].Filename != "" || string(parts[0].Data) != "make a diagram" {
		t.Fatalf("field part = %#v", parts[0])
	}
	if parts[1].Name != "image" || parts[1].Filename != "input.png" || parts[1].ContentType != "application/octet-stream" {
		t.Fatalf("file part = %#v", parts[1])
	}
}
