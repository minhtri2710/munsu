//go:build integration

package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
)

func TestQueryGLMergeStatusForStateRefusesUnknownState(t *testing.T) {
	_, err := queryGLMergeStatusForState(backend.State(99), &domain.DeliveryIdentity{})
	if err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("queryGLMergeStatusForState error = %v, want unknown-state refusal", err)
	}
}

func TestQueryDeliveryMergeStatus_RefusesNilIdentity(t *testing.T) {
	_, err := QueryDeliveryMergeStatus(nil)
	if err == nil || !strings.Contains(err.Error(), "delivery identity is nil") {
		t.Fatalf("QueryDeliveryMergeStatus error = %v, want nil-identity refusal", err)
	}
}

func TestQueryDeliveryMergeStatus_RefusesUnknownProvider(t *testing.T) {
	_, err := QueryDeliveryMergeStatus(&domain.DeliveryIdentity{Provider: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("QueryDeliveryMergeStatus error = %v, want unknown-provider refusal", err)
	}
}

func TestFetchProviderSnapshotForProviderRefusesUnknownProvider(t *testing.T) {
	_, err := fetchProviderSnapshotForProvider("unknown", "https://example.invalid")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("fetchProviderSnapshotForProvider error = %v, want unknown-provider refusal", err)
	}
}

func TestPRMergeStatus_JSONUnmarshal(t *testing.T) {
	// Test the domain.PRMergeStatus can be unmarshaled from gh CLI output
	input := `{"state":"MERGED","merged":true,"headRefOid":"abc123def456","mergedSha":"abc123def456"}`
	var status domain.PRMergeStatus
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.State != "MERGED" {
		t.Errorf("expected MERGED, got %s", status.State)
	}
	if !status.Merged {
		t.Error("expected merged=true")
	}
	if status.HeadSHA != "abc123def456" {
		t.Errorf("expected abc123def456, got %s", status.HeadSHA)
	}
	if status.MergedSHA != "abc123def456" {
		t.Errorf("expected mergedSha abc123def456, got %s", status.MergedSHA)
	}
}

func TestPRMergeStatus_Closed(t *testing.T) {
	input := `{"state":"CLOSED","merged":false,"headRefOid":"def456abc123","mergedSha":""}`
	var status domain.PRMergeStatus
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.State != "CLOSED" {
		t.Errorf("expected CLOSED, got %s", status.State)
	}
	if status.Merged {
		t.Error("expected merged=false")
	}
}

func TestPRMergeStatus_Open(t *testing.T) {
	input := `{"state":"OPEN","merged":false,"headRefOid":"xyz789","mergedSha":""}`
	var status domain.PRMergeStatus
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.State != "OPEN" {
		t.Errorf("expected OPEN, got %s", status.State)
	}
	if status.Merged {
		t.Error("expected merged=false")
	}
}

func TestPRMergeStatus_FieldTags(t *testing.T) {
	// Verify the JSON field tags match gh CLI output format
	var status domain.PRMergeStatus
	input := `{"state":"MERGED","merged":true,"headRefOid":"abc","mergedSha":"def"}`
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.HeadSHA != "abc" {
		t.Errorf("expected headRefOid to map to HeadSHA, got %s", status.HeadSHA)
	}
	if status.MergedSHA != "def" {
		t.Errorf("expected mergedSha to map to MergedSHA, got %s", status.MergedSHA)
	}
}
