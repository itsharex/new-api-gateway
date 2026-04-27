package gateway

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestTeeStreamCopiesToClientAndCapture(t *testing.T) {
	client := &bytes.Buffer{}
	capture := &bytes.Buffer{}
	input := bytes.NewBufferString("data: one\n\ndata: two\n\n")
	written, err := teeStream(input, client, capture)
	if err != nil {
		t.Fatalf("teeStream error: %v", err)
	}
	if written == 0 {
		t.Fatal("expected bytes written")
	}
	if client.String() != capture.String() {
		t.Fatalf("client=%q capture=%q", client.String(), capture.String())
	}
}

func TestTeeStreamPropagatesReadErrorAfterCopy(t *testing.T) {
	client := &bytes.Buffer{}
	capture := &bytes.Buffer{}
	_, err := teeStream(errorReader{}, client, capture)
	if err == nil {
		t.Fatal("expected error")
	}
}

type errorReader struct{}

func (errorReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestTeeStreamReturnsClientWriterError(t *testing.T) {
	clientErr := errors.New("client write failed")
	client := failingWriter{err: clientErr}
	capture := &bytes.Buffer{}
	_, err := teeStream(bytes.NewBufferString("abc"), client, capture)
	if !errors.Is(err, clientErr) {
		t.Fatalf("expected client error, got %v", err)
	}
	if capture.Len() != 0 {
		t.Fatalf("expected capture not to be written after client error, got %q", capture.String())
	}
}

func TestTeeStreamReturnsCaptureWriterError(t *testing.T) {
	captureErr := errors.New("capture write failed")
	client := &bytes.Buffer{}
	capture := failingWriter{err: captureErr}
	_, err := teeStream(bytes.NewBufferString("abc"), client, capture)
	if !errors.Is(err, captureErr) {
		t.Fatalf("expected capture error, got %v", err)
	}
	if client.String() != "abc" {
		t.Fatalf("expected client write before capture error, got %q", client.String())
	}
}

func TestTeeStreamReturnsShortWriteError(t *testing.T) {
	client := shortWriter{}
	capture := &bytes.Buffer{}
	_, err := teeStream(bytes.NewBufferString("abc"), client, capture)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("expected short write error, got %v", err)
	}
	if capture.Len() != 0 {
		t.Fatalf("expected capture not to be written after client short write, got %q", capture.String())
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write(p []byte) (int, error) { return 0, w.err }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
