package hometag

import (
	"testing"
)

func TestTagDeterministic(t *testing.T) {
	a := Tag("/home/user/.munsu")
	b := Tag("/home/user/.munsu")
	if a != b {
		t.Errorf("same input -> different tags: %q vs %q", a, b)
	}
}

func TestTagDifferent(t *testing.T) {
	a := Tag("/home/user/.munsu")
	b := Tag("/home/other/.munsu")
	if a == b {
		t.Errorf("different inputs -> same tag: %q", a)
	}
}

func TestTagLength(t *testing.T) {
	tag := Tag("/some/path")
	if len(tag) != 6 {
		t.Errorf("tag length = %d, want 6", len(tag))
	}
}

func TestTagHexChars(t *testing.T) {
	tag := Tag("/another/path")
	for _, c := range tag {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %q in tag %q", c, tag)
		}
	}
}
