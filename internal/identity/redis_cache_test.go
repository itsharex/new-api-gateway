package identity

import (
	"encoding/json"
	"testing"
)

func TestSnapshotJSONRoundTrip(t *testing.T) {
	original := Snapshot{TokenFingerprint: "fp", FingerprintDisplay: "tkfp_abc", EmployeeNo: "E12345", ResolutionStatus: "resolved"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.EmployeeNo != original.EmployeeNo {
		t.Fatalf("EmployeeNo = %q", decoded.EmployeeNo)
	}
}
