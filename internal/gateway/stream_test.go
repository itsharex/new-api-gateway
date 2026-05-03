package gateway

import (
	"bytes"
	"encoding/json"
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
	ex := newSSEUsageExtractor(&buf, extractorFor("openai_chat"))
	sse := "data: {\"id\":\"1\",\"model\":\"gpt-4o\"}\n\ndata: {\"id\":\"2\",\"model\":\"gpt-4o\",\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20,\"total_tokens\":30}}\n\ndata: [DONE]\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	u, m := ex.result()
	if u.TotalTokens != 30 {
		t.Fatalf("expected total_tokens=30, got %d", u.TotalTokens)
	}
	if u.PromptTokens != 10 {
		t.Fatalf("expected prompt_tokens=10, got %d", u.PromptTokens)
	}
	if u.CompletionTokens != 20 {
		t.Fatalf("expected completion_tokens=20, got %d", u.CompletionTokens)
	}
	if m != "gpt-4o" {
		t.Fatalf("expected model=gpt-4o, got %q", m)
	}
	if buf.String() != sse {
		t.Fatalf("passthrough mismatch: got %q", buf.String())
	}
}

func TestSSEUsageExtractorHandlesSplitChunks(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf, extractorFor("openai_chat"))
	chunk1 := "data: {\"id\":\"1\",\"model\":\"gpt-4"
	chunk2 := "o\",\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":8,\"total_tokens\":13}}\n\n"
	if _, err := ex.Write([]byte(chunk1)); err != nil {
		t.Fatalf("Write chunk1 error: %v", err)
	}
	u, _ := ex.result()
	if u.TotalTokens != 0 {
		t.Fatalf("expected 0 before complete line, got %d", u.TotalTokens)
	}
	if _, err := ex.Write([]byte(chunk2)); err != nil {
		t.Fatalf("Write chunk2 error: %v", err)
	}
	u, _ = ex.result()
	if u.TotalTokens != 13 {
		t.Fatalf("expected total_tokens=13, got %d", u.TotalTokens)
	}
}

func TestSSEUsageExtractorAnthropicFields(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf, extractorFor("claude_messages"))
	sse := "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":15}}\n\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	u, _ := ex.result()
	if u.PromptTokens != 100 {
		t.Fatalf("expected prompt_tokens=100, got %d", u.PromptTokens)
	}
	if u.CompletionTokens != 15 {
		t.Fatalf("expected completion_tokens=15, got %d", u.CompletionTokens)
	}
}

func TestSSEUsageExtractorEmptyStream(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf, extractorFor("openai_chat"))
	if _, err := ex.Write([]byte("")); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	u, _ := ex.result()
	if u.TotalTokens != 0 {
		t.Fatalf("expected 0, got %d", u.TotalTokens)
	}
}

func TestSSEUsageExtractorResponsesAPI(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf, extractorFor("openai_responses"))
	sse := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.2\",\"usage\":{\"input_tokens\":21903,\"output_tokens\":105,\"total_tokens\":22008,\"input_tokens_details\":{\"cached_tokens\":21760},\"output_tokens_details\":{\"reasoning_tokens\":74}}}}\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	u, m := ex.result()
	if u.PromptTokens != 21903 {
		t.Fatalf("expected prompt_tokens=21903, got %d", u.PromptTokens)
	}
	if u.CompletionTokens != 105 {
		t.Fatalf("expected completion_tokens=105, got %d", u.CompletionTokens)
	}
	if u.TotalTokens != 22008 {
		t.Fatalf("expected total_tokens=22008, got %d", u.TotalTokens)
	}
	if u.CachedTokens != 21760 {
		t.Fatalf("expected cached_tokens=21760, got %d", u.CachedTokens)
	}
	if u.ReasoningTokens != 74 {
		t.Fatalf("expected reasoning_tokens=74, got %d", u.ReasoningTokens)
	}
	if m != "gpt-5.2" {
		t.Fatalf("expected model=gpt-5.2, got %q", m)
	}
}

func TestSSEUsageExtractorResponsesAPIAssembled(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf, extractorFor("openai_responses"))
	sse := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.2\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}],\"usage\":{\"input_tokens\":100,\"output_tokens\":10,\"total_tokens\":110}}}\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	assembled := ex.assembledResult()
	if assembled == nil {
		t.Fatal("assembledResult returned nil")
	}
	var v struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(assembled, &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if v.ID != "resp_1" {
		t.Fatalf("id=%q", v.ID)
	}
	if v.Usage.TotalTokens != 110 {
		t.Fatalf("total_tokens=%d", v.Usage.TotalTokens)
	}
	if v.Output[0].Content[0].Text != "hi" {
		t.Fatalf("text=%q", v.Output[0].Content[0].Text)
	}
	if buf.String() != sse {
		t.Fatalf("passthrough mismatch")
	}
}

func TestSSEUsageExtractorChatCompletionsAssembled(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf, extractorFor("openai_chat"))
	sse := "data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1,\"total_tokens\":6}}\n\ndata: [DONE]\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	assembled := ex.assembledResult()
	if assembled == nil {
		t.Fatal("assembledResult returned nil")
	}
	var v struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(assembled, &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if v.ID != "chatcmpl-1" {
		t.Fatalf("id=%q", v.ID)
	}
	if v.Choices[0].Message.Content != "hi" {
		t.Fatalf("content=%q", v.Choices[0].Message.Content)
	}
	if v.Usage.TotalTokens != 6 {
		t.Fatalf("total_tokens=%d", v.Usage.TotalTokens)
	}
}
