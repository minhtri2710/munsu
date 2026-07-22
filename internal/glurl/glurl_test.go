package glurl

import (
	"testing"
)

func TestParseMRURL_Valid(t *testing.T) {
	gl, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gl.Host != "gitlab.com" {
		t.Errorf("Host: got %q, want %q", gl.Host, "gitlab.com")
	}
	if gl.Owner != "owner" {
		t.Errorf("Owner: got %q, want %q", gl.Owner, "owner")
	}
	if gl.Project != "project" {
		t.Errorf("Project: got %q, want %q", gl.Project, "project")
	}
	if gl.IID != 42 {
		t.Errorf("IID: got %d, want 42", gl.IID)
	}
}

func TestParseMRURL_SelfHosted(t *testing.T) {
	gl, err := ParseMRURL("https://gitlab.example.com/team/project/-/merge_requests/7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gl.Host != "gitlab.example.com" {
		t.Errorf("Host: got %q, want %q", gl.Host, "gitlab.example.com")
	}
	if gl.Owner != "team" {
		t.Errorf("Owner: got %q, want %q", gl.Owner, "team")
	}
	if gl.Project != "project" {
		t.Errorf("Project: got %q, want %q", gl.Project, "project")
	}
	if gl.IID != 7 {
		t.Errorf("IID: got %d, want 7", gl.IID)
	}
}

func TestParseMRURL_NestedGroup(t *testing.T) {
	gl, err := ParseMRURL("https://gitlab.com/group/subgroup/my-project/-/merge_requests/123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gl.Host != "gitlab.com" {
		t.Errorf("Host: got %q, want %q", gl.Host, "gitlab.com")
	}
	if gl.Owner != "group/subgroup" {
		t.Errorf("Owner: got %q, want %q", gl.Owner, "group/subgroup")
	}
	if gl.Project != "my-project" {
		t.Errorf("Project: got %q, want %q", gl.Project, "my-project")
	}
	if gl.IID != 123 {
		t.Errorf("IID: got %d, want 123", gl.IID)
	}
}

func TestParseMRURL_DeeplyNestedGroup(t *testing.T) {
	gl, err := ParseMRURL("https://gitlab.example.com/a/b/c/d/proj/-/merge_requests/99")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gl.Host != "gitlab.example.com" {
		t.Errorf("Host: got %q, want %q", gl.Host, "gitlab.example.com")
	}
	if gl.Owner != "a/b/c/d" {
		t.Errorf("Owner: got %q, want %q", gl.Owner, "a/b/c/d")
	}
	if gl.Project != "proj" {
		t.Errorf("Project: got %q, want %q", gl.Project, "proj")
	}
	if gl.IID != 99 {
		t.Errorf("IID: got %d, want 99", gl.IID)
	}
}

func TestParseMRURL_InvalidURL(t *testing.T) {
	_, err := ParseMRURL("not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestParseMRURL_NoPath(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com")
	if err == nil {
		t.Fatal("expected error for URL with no path")
	}
}

func TestParseMRURL_NonMRPath(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/owner/project/-/issues/42")
	if err == nil {
		t.Fatal("expected error for non-MR path")
	}
}

func TestParseMRURL_EmptyIID(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/")
	if err == nil {
		t.Fatal("expected error for empty IID")
	}
}

func TestParseMRURL_NonNumericIID(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/abc")
	if err == nil {
		t.Fatal("expected error for non-numeric IID")
	}
}

func TestParseMRURL_ZeroIID(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/0")
	if err == nil {
		t.Fatal("expected error for zero IID")
	}
}

func TestParseMRURL_NegativeIID(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/-1")
	if err == nil {
		t.Fatal("expected error for negative IID")
	}
}

func TestParseMRURL_MissingOwner(t *testing.T) {
	// This format is not valid — owner is required
	_, err := ParseMRURL("https://gitlab.com/project/-/merge_requests/1")
	if err == nil {
		t.Fatal("expected error for missing owner")
	}
}

func TestParseMRURL_TrailingSlash(t *testing.T) {
	gl, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/42/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gl.IID != 42 {
		t.Errorf("IID: got %d, want 42", gl.IID)
	}
}

func TestParseMRURL_GitHubURL(t *testing.T) {
	_, err := ParseMRURL("https://github.com/owner/repo/pull/42")
	if err == nil {
		t.Fatal("expected error for GitHub PR URL")
	}
}

func TestParseMRURL_NonHTTPS(t *testing.T) {
	_, err := ParseMRURL("http://gitlab.com/owner/project/-/merge_requests/42")
	if err == nil {
		t.Fatal("expected error for non-HTTPS URL")
	}
}

func TestParseMRURL_WithUserinfo(t *testing.T) {
	_, err := ParseMRURL("https://token@gitlab.com/owner/project/-/merge_requests/42")
	if err == nil {
		t.Fatal("expected error for URL with userinfo")
	}
}

func TestParseMRURL_WithQueryString(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/42?foo=bar")
	if err == nil {
		t.Fatal("expected error for URL with query string")
	}
}

func TestParseMRURL_WithFragment(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/42#section")
	if err == nil {
		t.Fatal("expected error for URL with fragment")
	}
}

func TestParseMRURL_ExtraPathSuffix(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/owner/project/-/merge_requests/42/foo")
	if err == nil {
		t.Fatal("expected error for URL with extra path suffix")
	}
}

func TestParseMRURL_EmptySegment(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/group//project/-/merge_requests/1")
	if err == nil {
		t.Fatal("expected error for URL with empty segment")
	}
}

func TestParseMRURL_DotSegment(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/group/./project/-/merge_requests/1")
	if err == nil {
		t.Fatal("expected error for URL with dot segment")
	}
}

func TestParseMRURL_DotDotSegment(t *testing.T) {
	_, err := ParseMRURL("https://gitlab.com/group/../project/-/merge_requests/1")
	if err == nil {
		t.Fatal("expected error for URL with dotdot segment")
	}
}

func TestParseMRURL_OpaqueURL(t *testing.T) {
	// Opaque URLs have no path
	_, err := ParseMRURL("opaque:gitlab.com/owner/project/-/merge_requests/1")
	if err == nil {
		t.Fatal("expected error for opaque URL")
	}
}

func TestParseMRURL_FormatMRRef(t *testing.T) {
	gl := GLURL{Host: "gitlab.com", Owner: "owner", Project: "project", IID: 42}
	ref := gl.FormatMRRef()
	if ref != "owner/project!42" {
		t.Errorf("FormatMRRef: got %q, want %q", ref, "owner/project!42")
	}
}

func TestFormatMRRef_NestedGroup(t *testing.T) {
	gl := GLURL{Host: "gitlab.com", Owner: "group/subgroup", Project: "my-project", IID: 7}
	ref := gl.FormatMRRef()
	if ref != "group/subgroup/my-project!7" {
		t.Errorf("FormatMRRef: got %q, want %q", ref, "group/subgroup/my-project!7")
	}
}

func TestFullURL(t *testing.T) {
	gl := GLURL{Host: "gitlab.com", Owner: "owner", Project: "project", IID: 42}
	url := gl.FullURL()
	if url != "https://gitlab.com/owner/project/-/merge_requests/42" {
		t.Errorf("FullURL: got %q, want %q", url, "https://gitlab.com/owner/project/-/merge_requests/42")
	}
}

func TestFullURL_SelfHosted(t *testing.T) {
	gl := GLURL{Host: "gitlab.example.com", Owner: "team", Project: "proj", IID: 7}
	url := gl.FullURL()
	if url != "https://gitlab.example.com/team/proj/-/merge_requests/7" {
		t.Errorf("FullURL: got %q, want %q", url, "https://gitlab.example.com/team/proj/-/merge_requests/7")
	}
}

func TestFullURL_NestedGroup(t *testing.T) {
	gl := GLURL{Host: "gitlab.com", Owner: "group/subgroup", Project: "my-project", IID: 123}
	url := gl.FullURL()
	if url != "https://gitlab.com/group/subgroup/my-project/-/merge_requests/123" {
		t.Errorf("FullURL: got %q, want %q", url, "https://gitlab.com/group/subgroup/my-project/-/merge_requests/123")
	}
}

func TestParseMRURL_AllFormatsRoundTrip(t *testing.T) {
	urls := []string{
		"https://gitlab.com/owner/project/-/merge_requests/1",
		"https://gitlab.example.com/team/project/-/merge_requests/42",
		"https://gitlab.com/group/subgroup/my-project/-/merge_requests/7",
		"https://gitlab.com/a/b/c/d/e/proj/-/merge_requests/99",
	}
	for _, url := range urls {
		gl, err := ParseMRURL(url)
		if err != nil {
			t.Fatalf("ParseMRURL(%q): %v", url, err)
		}
		reconstructed := gl.FullURL()
		if reconstructed != url {
			t.Errorf("round-trip %q -> %q", url, reconstructed)
		}
	}
}
