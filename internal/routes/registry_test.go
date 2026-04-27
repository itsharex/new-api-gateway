package routes

import "testing"

func TestDefaultRegistryMatchesImageGeneration(t *testing.T) {
	entry, ok := DefaultRegistry().Match("POST", "/v1/images/generations")
	if !ok {
		t.Fatal("expected route match")
	}
	if entry.ProtocolFamily != "openai_images" {
		t.Fatalf("ProtocolFamily = %q", entry.ProtocolFamily)
	}
	if entry.CaptureMode != CaptureRawAndNormalized {
		t.Fatalf("CaptureMode = %q", entry.CaptureMode)
	}
}

func TestDefaultRegistryMatchesMidjourneyRawMinimal(t *testing.T) {
	entry, ok := DefaultRegistry().Match("POST", "/mj/submit/imagine")
	if !ok {
		t.Fatal("expected route match")
	}
	if entry.CaptureMode != CaptureRawAndMinimal {
		t.Fatalf("CaptureMode = %q", entry.CaptureMode)
	}
}

func TestDefaultRegistryUnknownRoute(t *testing.T) {
	_, ok := DefaultRegistry().Match("POST", "/unknown/path")
	if ok {
		t.Fatal("expected no route match")
	}
}
