package fines

import (
	"errors"
	"testing"
)

func TestParseFineId_TrimsAndReturns(t *testing.T) {
	got, err := ParseFineId("  fine-123  ")
	if err != nil {
		t.Fatalf("ParseFineId: %v", err)
	}
	if got != FineId("fine-123") {
		t.Errorf("ParseFineId: got %q, want %q", got, "fine-123")
	}
}

func TestParseFineId_RejectsBlank(t *testing.T) {
	cases := []string{"", "   ", "\t\n"}
	for _, raw := range cases {
		_, err := ParseFineId(raw)
		if err == nil {
			t.Errorf("ParseFineId(%q): got nil, want *InvalidFineError", raw)
			continue
		}
		var invalid *InvalidFineError
		if !errors.As(err, &invalid) {
			t.Errorf("ParseFineId(%q) error: got %T(%v), want *InvalidFineError", raw, err, err)
		}
	}
}
