package cli

import (
	"embed"
	"encoding/json"
	"strings"
	"testing"
)

//go:embed contract_fixtures/capabilities.toon contract_fixtures/backend_capabilities.json
var outputFixtures embed.FS

func TestEncodeTOONMatchesCapabilitiesFixture(t *testing.T) {
	response := Response[Capabilities]{
		SchemaVersion: SchemaVersion,
		Kind:          "capabilities",
		Status:        "success",
		Data: Capabilities{
			ContractVersion: SchemaVersion,
			Commands: []string{
				"capabilities", "task observe", "fleet snapshot", "guard", "watch ensure",
				"watch run", "wake claim", "wake ack", "event append", "backend capabilities", "spawn",
			},
			OutputFormats: []string{OutputTOON, OutputJSON},
		},
		Help: []string{"Run `munsu task observe <task-id>` to inspect a task"},
	}

	got, err := Encode(response, OutputTOON)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	want := outputFixture(t, "capabilities.toon")
	if got != want {
		t.Errorf("TOON mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestEncodeJSONPreservesContractShape(t *testing.T) {
	response := Response[BackendCapabilities]{
		SchemaVersion: SchemaVersion,
		Kind:          "backend.capabilities",
		Status:        "success",
		Data: BackendCapabilities{
			Backend:  "tmux",
			Features: []string{"create_session", "send_input", "pane_liveness"},
		},
	}

	got, err := Encode(response, OutputJSON)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	var actual any
	if err := json.Unmarshal([]byte(got), &actual); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(outputFixture(t, "backend_capabilities.json")), &want); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(actual, want) {
		t.Errorf("JSON shape mismatch\nwant: %s\ngot: %s", outputFixture(t, "backend_capabilities.json"), got)
	}
}

func TestEncodeRejectsUnsupportedOutput(t *testing.T) {
	_, err := Encode(Response[Guard]{}, "yaml")
	if err == nil || !strings.Contains(err.Error(), "unsupported output") {
		t.Fatalf("Encode() error = %v, want unsupported output error", err)
	}
}

func outputFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := outputFixtures.ReadFile("contract_fixtures/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func jsonEqual(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func TestToonStringFollowsSpecEscapes(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"plain ascii stays bare", "task-1", "task-1"},
		{"non-ascii letters stay bare", "café", "café"},
		{"cjk stays bare", "日本", "日本"},
		{"quote is escaped", `a"b:`, `"a\"b:"`},
		{"backslash is escaped", `a\b:`, `"a\\b:"`},
		{"newline is escaped", "a\nb", `"a\nb"`},
		{"carriage return is escaped", "a\rb", `"a\rb"`},
		{"tab is escaped", "a\tb", `"a\tb"`},
		{"nul uses uXXXX", "a\x00b", `"a\u0000b"`},
		{"soh uses uXXXX", "a\x01b", `"a\u0001b"`},
		{"unit separator uses uXXXX", "a\x1fb", `"a\u001fb"`},
		{"non-ascii with a control is quoted, not escaped as hex", "é\x01", `"é\u0001"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toonString(tc.in); got != tc.want {
				t.Fatalf("toonString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
