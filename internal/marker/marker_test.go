package marker

import "testing"

func TestMarkFromMarshal_Idempotent(t *testing.T) {
	msg := "implement munsu-rank-rename"
	once := MarkFromMarshal(msg)
	twice := MarkFromMarshal(once)
	if once != twice {
		t.Fatalf("MarkFromMarshal not idempotent:\n once=%q\n twice=%q", once, twice)
	}
	if !IsFromMarshal(once) {
		t.Fatalf("marked message not recognized: %q", once)
	}
	if IsFromMarshal(msg) {
		t.Fatalf("plain message should not match marker")
	}
	if once == msg {
		t.Fatalf("expected prefix, got unchanged message")
	}
	if IsFromMarshal(FromMarshalLabel + "no-separator") {
		t.Fatalf("label without invisible separator must not match")
	}
}
