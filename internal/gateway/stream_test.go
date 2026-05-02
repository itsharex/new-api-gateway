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

func TestSSEUsageExtractorExtractsUsageFromLastChunk(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf)
	sse := "data: {\"id\":\"1\",\"model\":\"gpt-4o\"}\n\ndata: {\"id\":\"2\",\"model\":\"gpt-4o\",\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20,\"total_tokens\":30}}\n\ndata: [DONE]\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if ex.usage.TotalTokens != 30 {
		t.Fatalf("expected total_tokens=30, got %d", ex.usage.TotalTokens)
	}
	if ex.usage.PromptTokens != 10 {
		t.Fatalf("expected prompt_tokens=10, got %d", ex.usage.PromptTokens)
	}
	if ex.usage.CompletionTokens != 20 {
		t.Fatalf("expected completion_tokens=20, got %d", ex.usage.CompletionTokens)
	}
	if ex.model != "gpt-4o" {
		t.Fatalf("expected model=gpt-4o, got %q", ex.model)
	}
	if buf.String() != sse {
		t.Fatalf("passthrough mismatch: got %q", buf.String())
	}
}

func TestSSEUsageExtractorHandlesSplitChunks(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf)
	chunk1 := "data: {\"id\":\"1\",\"model\":\"gpt-4"
	chunk2 := "o\",\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":8,\"total_tokens\":13}}\n\n"
	if _, err := ex.Write([]byte(chunk1)); err != nil {
		t.Fatalf("Write chunk1 error: %v", err)
	}
	if ex.usage.TotalTokens != 0 {
		t.Fatalf("expected 0 before complete line, got %d", ex.usage.TotalTokens)
	}
	if _, err := ex.Write([]byte(chunk2)); err != nil {
		t.Fatalf("Write chunk2 error: %v", err)
	}
	if ex.usage.TotalTokens != 13 {
		t.Fatalf("expected total_tokens=13, got %d", ex.usage.TotalTokens)
	}
}

func TestSSEUsageExtractorAnthropicFields(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf)
	sse := "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":15}}\n\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if ex.usage.PromptTokens != 100 {
		t.Fatalf("expected prompt_tokens=100, got %d", ex.usage.PromptTokens)
	}
}

func TestSSEUsageExtractorEmptyStream(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf)
	if _, err := ex.Write([]byte("")); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if ex.usage.TotalTokens != 0 {
		t.Fatalf("expected 0, got %d", ex.usage.TotalTokens)
	}
}

func TestSSEUsageExtractorFallbackTotalTokens(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf)
	sse := "data: {\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if ex.usage.PromptTokens != 7 {
		t.Fatalf("expected prompt_tokens=7, got %d", ex.usage.PromptTokens)
	}
	if ex.usage.CompletionTokens != 3 {
		t.Fatalf("expected completion_tokens=3, got %d", ex.usage.CompletionTokens)
	}
	if ex.usage.TotalTokens != 10 {
		t.Fatalf("expected total_tokens=10, got %d", ex.usage.TotalTokens)
	}
}
