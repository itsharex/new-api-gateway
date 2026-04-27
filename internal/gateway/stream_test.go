package gateway

import (
	"bytes"
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
