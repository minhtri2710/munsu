package home

import "testing"

func TestMarkFromGeneral_Idempotent(t *testing.T) {
	msg := "implement munsu-rank-rename"
	once := MarkFromGeneral(msg)
	twice := MarkFromGeneral(once)
	if once != twice {
		t.Fatalf("MarkFromGeneral not idempotent:\n once=%q\n twice=%q", once, twice)
	}
	if !IsFromGeneral(once) {
		t.Fatalf("marked message not recognized: %q", once)
	}
	if IsFromGeneral(msg) {
		t.Fatalf("plain message should not match marker")
	}
	if once == msg {
		t.Fatalf("expected prefix, got unchanged message")
	}
	if IsFromGeneral(FromGeneralLabel + "no-separator") {
		t.Fatalf("label without invisible separator must not match")
	}
}
