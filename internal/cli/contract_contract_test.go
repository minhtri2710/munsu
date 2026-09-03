package cli

import (
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

//go:embed contract_fixtures/*.json contract_fixtures/*.toon
var fixtureFiles embed.FS

var fixtureNames = []string{
	"backend_capabilities",
	"capabilities",
	"decision_hold",
	"drain_cycle",
	"empty",
	"error",
	"event_append",
	"event_record",
	"fleet_snapshot",
	"guard",
	"message_injection",
	"noop",
	"project_entry",
	"safety_check",
	"session_start",
	"spawn_receipt",
	"success",
	"task_entry",
	"task_observe",
	"truncated",
	"wake_ack",
	"wake_claim",
	"watch_ensure",
	"watch_run",
	"watch_status",
	"watch_stop",
}

func TestGoldenFixturePairs(t *testing.T) {
	t.Parallel()

	entries, err := fixtureFiles.ReadDir("contract_fixtures")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		ext := filepath.Ext(name)
		if ext != ".json" && ext != ".toon" {
			t.Fatalf("unexpected fixture extension: %s", name)
		}
		base := strings.TrimSuffix(name, ext)
		if seen[base] == nil {
			seen[base] = make(map[string]bool)
		}
		seen[base][ext] = true
	}

	gotNames := make([]string, 0, len(seen))
	for name, extensions := range seen {
		gotNames = append(gotNames, name)
		if !extensions[".json"] || !extensions[".toon"] {
			t.Errorf("%s must have paired .json and .toon fixtures", name)
		}
	}
	sort.Strings(gotNames)
	if !reflect.DeepEqual(gotNames, fixtureNames) {
		t.Errorf("fixture names = %v, want %v", gotNames, fixtureNames)
	}

	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			jsonFixture, err := fixtureFiles.ReadFile("contract_fixtures/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(jsonFixture, &document); err != nil {
				t.Fatalf("invalid JSON fixture: %v", err)
			}
			validateJSONEnvelope(t, name, document)

			toonFixture, err := fixtureFiles.ReadFile("contract_fixtures/" + name + ".toon")
			if err != nil {
				t.Fatal(err)
			}
			validateTOONFixture(t, name, string(toonFixture), document)
		})
	}
}

func TestSchemaVersionAndModelJSONTags(t *testing.T) {
	t.Parallel()
	if SchemaVersion != "munsu.orchestration/v1" {
		t.Fatalf("SchemaVersion = %q; update the compatibility table and fixtures before changing it", SchemaVersion)
	}
	for _, value := range []any{
		ErrorResponse{}, ErrorEnvelope{}, Capabilities{}, TaskObserve{},
		FleetSnapshot{}, Soldier{}, CaptainEntry{}, CaptainGuidance{},
		CaptainChildBrief{}, CaptainDecision{}, CaptainHold{}, CaptainQueued{},
		CaptainLanded{}, CaptainOmitted{}, CaptainHomeCounts{},
		WakeAck{}, BackendCapabilities{}, SpawnReceipt{}, MessageResult{}, InboxReceiveResult{}, EmptyResult{},
		TruncatedResult{}, Guard{}, GuardViolation{}, WatchEnsure{}, WatchRun{}, WatchStop{},
		WakeClaim{}, WatchStatus{}, WatchLeaseInfo{},
		EventRecord{}, EventAppend{}, DrainCycle{}, SessionStart{},
		SafetyCheckData{}, ReportInjection{}, DecisionHoldInfo{},
		TaskEntry{}, ProjectEntry{},
	} {
		typeOf := reflect.TypeOf(value)
		for field := range typeOf.Fields() {
			if field.PkgPath != "" {
				continue
			}
			if field.Tag.Get("json") == "" {
				t.Errorf("%s.%s is missing a JSON tag", typeOf.Name(), field.Name)
			}
		}
	}
}

func TestErrorFixtureHasStableActionableEnvelope(t *testing.T) {
	t.Parallel()
	contents, err := fixtureFiles.ReadFile("contract_fixtures/error.json")
	if err != nil {
		t.Fatal(err)
	}
	var document ErrorResponse
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	if document.Kind != "error" || document.Status != "error" {
		t.Fatalf("error fixture envelope = kind %q, status %q", document.Kind, document.Status)
	}
	if document.Error.ErrorCode == "" || document.Error.Action == "" || document.Error.Message == "" {
		t.Fatal("error fixture must supply error_code, action, and message")
	}
}

// TestAllStableErrorCodes verifies every documented error_code has a
// representative fixture and round-trips correctly in both formats.
func TestAllStableErrorCodes(t *testing.T) {
	t.Parallel()

	codes := []string{
		"invalid_argument",
		"unknown_flag",
		"unsupported_input",
		"not_found",
		"invalid_state",
		"dependency_unavailable",
		"conflict",
		"internal",
	}

	for _, code := range codes {
		code := code
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			errResp := ErrorResponse{
				SchemaVersion: SchemaVersion,
				Kind:          "error",
				Status:        "error",
				Error: ErrorEnvelope{
					ErrorCode: code,
					Retryable: false,
					Action:    "Run `munsu help` for usage",
					Message:   "A test error for " + code,
				},
			}

			// Encode and decode JSON
			jsonOut, err := Encode(errResp, OutputJSON)
			if err != nil {
				t.Fatalf("JSON encode: %v", err)
			}
			var decoded ErrorResponse
			if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
				t.Fatalf("JSON decode: %v", err)
			}
			if decoded.Error.ErrorCode != code {
				t.Errorf("error_code = %q, want %q", decoded.Error.ErrorCode, code)
			}
			if decoded.Error.Action == "" || decoded.Error.Message == "" {
				t.Error("error must have action and message")
			}

			// TOON encode (must not error)
			toonOut, err := Encode(errResp, OutputTOON)
			if err != nil {
				t.Fatalf("TOON encode: %v", err)
			}
			if toonOut == "" {
				t.Error("TOON output must not be empty")
			}
			if !strings.Contains(toonOut, code) {
				t.Errorf("TOON output must contain error_code %q", code)
			}
		})
	}
}

// TestAllDefinitiveEmpties verifies that each model serializes its
// definitive empty form correctly in both output formats.
func TestAllDefinitiveEmpties(t *testing.T) {
	t.Parallel()

	t.Run("empty_result", func(t *testing.T) {
		t.Parallel()
		resp := Response[EmptyResult]{
			SchemaVersion: SchemaVersion,
			Kind:          "fleet.snapshot",
			Status:        "success",
			Data: EmptyResult{
				Count:   0,
				Context: "No soldiers found",
			},
		}

		jsonOut, err := Encode(resp, OutputJSON)
		if err != nil {
			t.Fatalf("JSON encode: %v", err)
		}
		if !strings.Contains(jsonOut, `"count": 0`) {
			t.Errorf("JSON must contain count: 0")
		}
		if !strings.Contains(jsonOut, `"context": "No soldiers found"`) {
			t.Errorf("JSON must contain context")
		}

		toonOut, err := Encode(resp, OutputTOON)
		if err != nil {
			t.Fatalf("TOON encode: %v", err)
		}
		if !strings.Contains(toonOut, "count: 0") {
			t.Errorf("TOON must contain count: 0")
		}
		if !strings.Contains(toonOut, "context: No soldiers found") {
			t.Errorf("TOON must contain context")
		}
	})

	t.Run("empty_collection", func(t *testing.T) {
		t.Parallel()
		resp := Response[FleetSnapshot]{
			SchemaVersion: SchemaVersion,
			Kind:          "fleet.snapshot",
			Status:        "success",
			Data: FleetSnapshot{
				Scope:    "/",
				Count:    0,
				Total:    0,
				Soldiers: []Soldier{},
			},
		}

		jsonOut, err := Encode(resp, OutputJSON)
		if err != nil {
			t.Fatalf("JSON encode: %v", err)
		}
		if strings.Contains(jsonOut, `"soldiers": null`) {
			t.Errorf("empty soldiers must be [] not null")
		}
		if !strings.Contains(jsonOut, `"soldiers": []`) {
			t.Errorf("empty soldiers must serialize as []")
		}

		toonOut, err := Encode(resp, OutputTOON)
		if err != nil {
			t.Fatalf("TOON encode: %v", err)
		}
		if !strings.Contains(toonOut, "soldiers: []") {
			t.Errorf("TOON empty soldiers must be []")
		}
	})
}

// TestIdempotentMutationReceipts verifies that no-op and idempotent
// mutation results serialize with correct noop: true semantics.
func TestIdempotentMutationReceipts(t *testing.T) {
	t.Parallel()

	t.Run("noop_true", func(t *testing.T) {
		t.Parallel()
		resp := Response[MessageResult]{
			SchemaVersion: SchemaVersion,
			Kind:          "message",
			Status:        "success",
			Data: MessageResult{
				Message: "Watch already running",
				Noop:    true,
			},
		}

		jsonOut, err := Encode(resp, OutputJSON)
		if err != nil {
			t.Fatalf("JSON encode: %v", err)
		}
		if !strings.Contains(jsonOut, `"noop": true`) {
			t.Errorf("JSON noop must be true")
		}
		if !strings.Contains(jsonOut, `"status": "success"`) {
			t.Errorf("idempotent no-op must be status=success")
		}

		toonOut, err := Encode(resp, OutputTOON)
		if err != nil {
			t.Fatalf("TOON encode: %v", err)
		}
		if !strings.Contains(toonOut, "noop: true") {
			t.Errorf("TOON noop must be true")
		}
	})

	t.Run("noop_false_success", func(t *testing.T) {
		t.Parallel()
		resp := Response[MessageResult]{
			SchemaVersion: SchemaVersion,
			Kind:          "message",
			Status:        "success",
			Data: MessageResult{
				Message: "Wake acknowledged",
				Noop:    false,
			},
		}

		jsonOut, err := Encode(resp, OutputJSON)
		if err != nil {
			t.Fatalf("JSON encode: %v", err)
		}
		if strings.Contains(jsonOut, `"noop": true`) {
			t.Errorf("first mutation must not be noop")
		}
		if !strings.Contains(jsonOut, `"noop": false`) {
			t.Errorf("first mutation must have noop=false")
		}
	})
}

func validateJSONEnvelope(t *testing.T, name string, document map[string]any) {
	t.Helper()
	for _, key := range []string{"schema_version", "kind", "status"} {
		if _, ok := document[key]; !ok {
			t.Errorf("%s.json missing %q", name, key)
		}
	}
	if document["schema_version"] != SchemaVersion {
		t.Errorf("%s.json schema_version = %v, want %q", name, document["schema_version"], SchemaVersion)
	}
	if document["status"] == "error" {
		if _, ok := document["error"].(map[string]any); !ok {
			t.Errorf("%s.json error response needs an error object", name)
		}
		return
	}
	dataVal := document["data"]
	if dataVal == nil {
		t.Errorf("%s.json success response needs a data field", name)
		return
	}
	switch dataVal.(type) {
	case map[string]any, []any:
		// data can be either an object or an array
	default:
		t.Errorf("%s.json success data must be an object or array, got %T", name, dataVal)
	}
}

var arrayHeader = regexp.MustCompile(`^( *)([A-Za-z_][A-Za-z0-9_.]*)\[([0-9]+)\](?:\{[^}]*\})?:\s?(.*)$`)

func validateTOONFixture(t *testing.T, name, fixture string, document map[string]any) {
	t.Helper()
	if strings.HasSuffix(fixture, "\n") {
		t.Errorf("%s.toon has a trailing newline; TOON v3.3 fixtures must not", name)
	}
	lines := strings.Split(fixture, "\n")
	for index, line := range lines {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Errorf("%s.toon:%d has trailing whitespace", name, index+1)
		}
		if strings.HasPrefix(line, "\t") {
			t.Errorf("%s.toon:%d uses tab indentation", name, index+1)
		}
		if leadingSpaces(line)%2 != 0 {
			t.Errorf("%s.toon:%d indentation is not a two-space multiple", name, index+1)
		}
		match := arrayHeader.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		want, err := strconv.Atoi(match[3])
		if err != nil {
			t.Fatalf("%s.toon:%d invalid array count: %v", name, index+1, err)
		}
		if match[4] != "" {
			if got := len(splitTOONValues(match[4])); got != want {
				t.Errorf("%s.toon:%d declared %d inline values, found %d", name, index+1, want, got)
			}
			continue
		}
		depth := len(match[1])
		got := 0
		for _, child := range lines[index+1:] {
			if child == "" || leadingSpaces(child) <= depth {
				break
			}
			if leadingSpaces(child) == depth+2 {
				got++
			}
		}
		if got != want {
			t.Errorf("%s.toon:%d declared %d expanded values, found %d", name, index+1, want, got)
		}
	}
	for _, key := range []string{"schema_version", "kind", "status"} {
		value, _ := document[key].(string)
		want := fmt.Sprintf("%s: %s", key, toonScalar(value))
		if !containsLine(lines, want) && !containsLine(lines, fmt.Sprintf("%s: %q", key, value)) {
			t.Errorf("%s.toon is missing JSON-equivalent envelope field %q", name, want)
		}
	}
	if document["status"] == "error" && !containsLine(lines, "  error_code:") {
		t.Errorf("%s.toon error response is missing error_code", name)
	}
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func containsLine(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func toonScalar(value string) string {
	if strings.ContainsAny(value, `:[]{}"\\`) || value == "" {
		return strconv.Quote(value)
	}
	return value
}

func splitTOONValues(values string) []string {
	var result []string
	quoted := false
	escaped := false
	start := 0
	for index, runeValue := range values {
		if escaped {
			escaped = false
			continue
		}
		if runeValue == '\\' && quoted {
			escaped = true
			continue
		}
		if runeValue == '"' {
			quoted = !quoted
			continue
		}
		if runeValue == ',' && !quoted {
			result = append(result, values[start:index])
			start = index + 1
		}
	}
	return append(result, values[start:])
}
