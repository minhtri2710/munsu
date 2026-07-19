package contract

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

//go:embed fixtures/*.json fixtures/*.toon
var fixtureFiles embed.FS

var fixtureNames = []string{
	"backend_capabilities",
	"capabilities",
	"empty",
	"error",
	"fleet_snapshot_v2",
	"guard",
	"noop",
	"spawn_receipt",
	"success",
	"task_observe",
	"truncated",
	"wake_ack",
	"wake_claim",
	"watch_ensure",
	"watch_run",
	"watch_stop",
}

func TestGoldenFixturePairs(t *testing.T) {
	t.Parallel()

	entries, err := fixtureFiles.ReadDir("fixtures")
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
			jsonFixture, err := fixtureFiles.ReadFile("fixtures/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(jsonFixture, &document); err != nil {
				t.Fatalf("invalid JSON fixture: %v", err)
			}
			validateJSONEnvelope(t, name, document)

			toonFixture, err := fixtureFiles.ReadFile("fixtures/" + name + ".toon")
			if err != nil {
				t.Fatal(err)
			}
			validateTOONFixture(t, name, string(toonFixture), document)
		})
	}
}

func TestSchemaVersionAndModelJSONTags(t *testing.T) {
	t.Parallel()
	if SchemaVersion != "munsu.orchestration/v2" {
		t.Fatalf("SchemaVersion = %q; update the compatibility table and fixtures before changing it", SchemaVersion)
	}
	for _, value := range []any{
		ErrorResponse{}, ErrorEnvelope{}, Capabilities{}, TaskObserve{},
		FleetSnapshotV2{}, Soldier{}, CaptainEntry{},
		WakeAck{}, BackendCapabilities{}, SpawnReceipt{}, MessageResult{}, EmptyResult{},
		TruncatedResult{}, Guard{}, WatchEnsure{}, WatchRun{}, WatchStop{}, WakeClaim{},
		EventRecord{}, EventAppend{},
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
	contents, err := fixtureFiles.ReadFile("fixtures/error.json")
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
	if _, ok := document["data"].(map[string]any); !ok {
		t.Errorf("%s.json success response needs a data object", name)
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
