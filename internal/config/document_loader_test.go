package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFleetBaseLoaderRejectsInvalidRecords(t *testing.T) {
	base := validBase()
	base.Config = ProjectOverlay{RequireNoMistakes: &[]bool{true}[0]}
	for _, tc := range []struct {
		name  string
		path  string
		value any
		load  func(string) error
		want  string
	}{
		{name: "base", path: BaseDocumentPath, value: base, load: func(home string) error { _, err := LoadFleetBase(home); return err }, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			data, err := marshalDocument(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := tc.load(home); err != nil {
				t.Fatalf("load error = %v, want nil", err)
			}
		})
	}
}

func TestFleetBaseLoaderIsStrict(t *testing.T) {
	base := validBase()
	cases := []struct {
		name  string
		path  string
		valid any
		load  func(string) error
	}{
		{name: "base", path: BaseDocumentPath, valid: base, load: func(home string) error { _, err := LoadFleetBase(home); return err }},
	}
	for _, tc := range cases {
		for _, defect := range []struct {
			name    string
			content func([]byte) []byte
			want    string
		}{
			{name: "missing schema", content: removeSchemaVersion, want: "schemaVersion"},
			{name: "unsupported schema", content: func(data []byte) []byte {
				return []byte(strings.Replace(string(data), schemaVersionOf(tc.valid), "future", 1))
			}, want: "schemaVersion"},
			{name: "unknown field", content: func(data []byte) []byte { return []byte(strings.Replace(string(data), "{", `{"unknown":true,`, 1)) }, want: "unknown field"},
			{name: "trailing JSON", content: func(data []byte) []byte { return append(data, []byte("\n{}")...) }, want: "trailing JSON"},
		} {
			t.Run(tc.name+"/"+defect.name, func(t *testing.T) {
				home := t.TempDir()
				data, err := marshalDocument(tc.valid)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(home, tc.path)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, defect.content(data), 0600); err != nil {
					t.Fatal(err)
				}
				if err := tc.load(home); err == nil || !strings.Contains(err.Error(), defect.want) {
					t.Fatalf("load error = %v, want %q", err, defect.want)
				}
			})
		}
	}
}

func removeSchemaVersion(data []byte) []byte {
	text := string(data)
	start := strings.Index(text, `"schemaVersion"`)
	if start < 0 {
		return data
	}
	end := strings.Index(text[start:], ",")
	if end < 0 {
		return data
	}
	return []byte(text[:start] + text[start+end+1:])
}

func schemaVersionOf(value any) string {
	return FleetBaseSchemaVersion
}

func marshalDocument(value any) ([]byte, error) {
	home, err := os.MkdirTemp("", "munsu-config-test-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	path := filepath.Join(home, "document.json")
	if err := storeDocument(path, value); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}