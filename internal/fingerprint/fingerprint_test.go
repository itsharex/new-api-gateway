package fingerprint

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestComputeStableFingerprint(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	first := Compute("abc123", secret)
	second := Compute("abc123", secret)
	if first.Value != second.Value {
		t.Fatalf("fingerprints differ: %q != %q", first.Value, second.Value)
	}
	if first.Display != second.Display {
		t.Fatalf("display fingerprints differ: %q != %q", first.Display, second.Display)
	}
}

func TestComputeMatchesKnownVectors(t *testing.T) {
	tests := []struct {
		name        string
		canonical   string
		secret      string
		wantValue   string
		wantDisplay string
	}{
		{
			name:        "basic canonical key",
			canonical:   "abc123",
			secret:      "0123456789abcdef0123456789abcdef",
			wantValue:   "0bc7cba04ce6b95777387ecfc565e941ecd97bbbd213b40e13e77ab64cc02503",
			wantDisplay: "tkfp_bpd4xicm424v",
		},
		{
			name:        "hyphenated canonical key",
			canonical:   "abc123-extra",
			secret:      "0123456789abcdef0123456789abcdef",
			wantValue:   "4ec50af351b43f694288ae153a4815c635d5a7dc35c066bf2f7d2f48bf1cc259",
			wantDisplay: "tkfp_j3cqv42rwq7w",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := Compute(tt.canonical, tt.secret)
			if fp.Value != tt.wantValue {
				t.Fatalf("Value = %q, want %q", fp.Value, tt.wantValue)
			}
			if fp.Display != tt.wantDisplay {
				t.Fatalf("Display = %q, want %q", fp.Display, tt.wantDisplay)
			}
			assertFingerprintFormat(t, fp)
		})
	}
}

func TestComputeChangesWithSecret(t *testing.T) {
	first := Compute("abc123", "0123456789abcdef0123456789abcdef")
	second := Compute("abc123", "abcdef0123456789abcdef0123456789")
	if first.Value == second.Value {
		t.Fatal("expected different fingerprints for different secrets")
	}
}

func assertFingerprintFormat(t *testing.T, fp Fingerprint) {
	t.Helper()
	if len(fp.Value) != 64 {
		t.Fatalf("Value length = %d, want 64", len(fp.Value))
	}
	if _, err := hex.DecodeString(fp.Value); err != nil {
		t.Fatalf("Value is not valid hex: %v", err)
	}
	if len(fp.Display) != 17 {
		t.Fatalf("Display length = %d, want 17", len(fp.Display))
	}
	if !strings.HasPrefix(fp.Display, "tkfp_") {
		t.Fatalf("Display = %q, want tkfp_ prefix", fp.Display)
	}
	for _, ch := range strings.TrimPrefix(fp.Display, "tkfp_") {
		if (ch < 'a' || ch > 'z') && (ch < '2' || ch > '7') {
			t.Fatalf("Display contains non-base32 character %q", ch)
		}
	}
}
