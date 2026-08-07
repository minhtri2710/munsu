package home

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriterInventory is the quiescence evidence returned by a writer fence.
// The fleet writer fence (internal/fleet) produces it after verifying no
// typed artifacts or OS writer processes remain for a home.
type WriterInventory struct {
	VerifiedQuiescent bool     `json:"verified_quiescent"`
	Writers           []string `json:"writers,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
}

type LegacyWriterPreflight struct{}

func (LegacyWriterPreflight) Inspect(homeDir string) (WriterInventory, error) {
	inventory, err := inventoryLegacyWriters(homeDir)
	if err != nil {
		return inventory, err
	}
	inventory.VerifiedQuiescent = false
	if len(inventory.Writers) != 0 {
		return inventory, fmt.Errorf("legacy writer artifacts or endpoints remain: %s", strings.Join(inventory.Writers, ", "))
	}
	inventory.Evidence = append(inventory.Evidence, "artifact preflight is empty; OS process proof is still required")
	return inventory, nil
}

func inventoryLegacyWriters(homeDir string) (WriterInventory, error) {
	stateDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return WriterInventory{}, nil
		}
		return WriterInventory{}, err
	}
	inventory := WriterInventory{}
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case ".watcher-identity", ".lock", ".watch.lock", ".afk.lock", ".afk-daemon.lock":
			inventory.Writers = append(inventory.Writers, "state/"+name)
			continue
		}
		if !strings.HasSuffix(name, ".meta") || !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(stateDir, name)
		meta, err := readLegacyMeta(path)
		if err != nil {
			return inventory, err
		}
		if meta["window"] != "" || meta["herdr_pane_id"] != "" {
			inventory.Writers = append(inventory.Writers, "state/"+name+":endpoint")
		}
	}
	sort.Strings(inventory.Writers)
	return inventory, nil
}

func readLegacyMeta(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	meta := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return meta, nil
}
