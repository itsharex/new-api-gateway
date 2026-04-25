package fingerprint

import "testing"

func TestComputeStableFingerprint(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	first := Compute("abc123", secret)
	second := Compute("abc123", secret)
	if first.Value != second.Value {
		t.Fatalf("fingerprints differ: %q != %q", first.Value, second.Value)
	}
	if first.Display == "" || first.Display[:5] != "tkfp_" {
		t.Fatalf("unexpected display %q", first.Display)
	}
}

func TestComputeChangesWithSecret(t *testing.T) {
	first := Compute("abc123", "0123456789abcdef0123456789abcdef")
	second := Compute("abc123", "abcdef0123456789abcdef0123456789")
	if first.Value == second.Value {
		t.Fatal("expected different fingerprints for different secrets")
	}
}
