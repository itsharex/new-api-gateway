package employee

import (
	"regexp"
	"testing"
)

func TestNormalizeEmployeeNo(t *testing.T) {
	got := Normalize(" e12345 ")
	if got != "E12345" {
		t.Fatalf("Normalize returned %q", got)
	}
}

func TestValidateEmployeeNo(t *testing.T) {
	pattern := regexp.MustCompile(`^[A-Z][0-9]{5}$`)
	if err := Validate("E12345", pattern); err != nil {
		t.Fatalf("expected valid employee no: %v", err)
	}
	if err := Validate("ZhangSan", pattern); err == nil {
		t.Fatal("expected invalid employee no")
	}
}
