package home

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const wakeResolutionMigrationDir = "state/.wake-resolution-migration"

var wakeResolutionMigrationCrashAfter string

type WakeResolutionMigrationPlan struct {
	SchemaVersion        string                          `json:"schema_version"`
	HomeDir              string                          `json:"home_dir"`
	HomeIdentity         string                          `json:"home_identity"`
	SourcePath           string                          `json:"source_path"`
	SourceDigest         string                          `json:"source_digest"`
	RecordCount          int                             `json:"record_count"`
	Outputs              []WakeResolutionMigrationOutput `json:"outputs"`
	OutputManifestDigest string                          `json:"output_manifest_digest"`
}

type WakeResolutionMigrationOutput struct {
	FileName string `json:"file_name"`
	Digest   string `json:"digest"`
	LeaseID  string `json:"lease_id"`
	EventID  string `json:"event_id"`
}

type WakeResolutionMigrationReceipt struct {
	HomeDir              string `json:"home_dir"`
	HomeIdentity         string `json:"home_identity"`
	SourcePath           string `json:"source_path"`
	SourceDigest         string `json:"source_digest"`
	ArchivePath          string `json:"archive_path"`
	RecordCount          int    `json:"record_count"`
	OutputManifestDigest string `json:"output_manifest_digest"`
	CompletedAt          int64  `json:"completed_at"`
}

func WriteWakeResolutionMigrationPlan(path string, plan *WakeResolutionMigrationPlan) error {
	if err := validateWakeResolutionPlanShape(plan); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != string(data) {
			return fmt.Errorf("wake resolution migration plan conflict at %s", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func ReadWakeResolutionMigrationPlan(path string) (*WakeResolutionMigrationPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var plan WakeResolutionMigrationPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if err := validateWakeResolutionPlanShape(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func WakeResolutionMigrationCommand(homeDir string) string {
	return "munsu migrate wake-resolutions plan --home " + shellQuote(homeDir) + " --plan-out " + shellQuote(defaultWakeResolutionPlanOut(homeDir))
}

func legacyWakeResolutionError(homeDir string) error {
	return fmt.Errorf("legacy wake resolution state requires migration; run: %s", WakeResolutionMigrationCommand(homeDir))
}

func hasLegacyWakeResolutionState(homeDir string) (bool, error) {
	info, err := os.Stat(filepath.Join(homeDir, wakeResolutionDir))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !info.IsDir(), nil
}

func PlanWakeResolutionMigration(homeDir string) (*WakeResolutionMigrationPlan, error) {
	canonHome, err := canonicalWakeResolutionHome(homeDir)
	if err != nil {
		return nil, err
	}
	legacy, err := hasLegacyWakeResolutionState(canonHome)
	if err != nil {
		return nil, err
	}
	if !legacy {
		return nil, fmt.Errorf("legacy wake resolution state not found at %s", filepath.Join(canonHome, wakeResolutionDir))
	}
	path := filepath.Join(canonHome, wakeResolutionDir)
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read legacy wake resolutions: %w", err)
	}
	records, outputs, manifestDigest, err := planWakeResolutionOutputs(source)
	if err != nil {
		return nil, err
	}
	identity, err := wakeResolutionHomeIdentity(canonHome)
	if err != nil {
		return nil, err
	}
	return &WakeResolutionMigrationPlan{
		SchemaVersion:        "munsu.wake-resolution-migration/v1",
		HomeDir:              canonHome,
		HomeIdentity:         identity,
		SourcePath:           path,
		SourceDigest:         digestHex(source),
		RecordCount:          len(records),
		Outputs:              outputs,
		OutputManifestDigest: manifestDigest,
	}, nil
}

func ApplyWakeResolutionMigration(plan *WakeResolutionMigrationPlan) (*WakeResolutionMigrationReceipt, error) {
	if plan == nil {
		return nil, fmt.Errorf("wake resolution migration plan is required")
	}
	if err := validateWakeResolutionPlanShape(plan); err != nil {
		return nil, err
	}
	receiptPath := wakeResolutionMigrationReceiptPath(plan.HomeDir)
	if receipt, err := readWakeResolutionMigrationReceipt(receiptPath); err == nil {
		if err := verifyWakeResolutionReceiptMatchesPlan(receipt, plan); err != nil {
			return nil, err
		}
		if err := verifyWakeResolutionMigrationComplete(plan, receipt); err != nil {
			return nil, err
		}
		return receipt, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := verifyWakeResolutionPlanIdentity(plan); err != nil {
		return nil, err
	}

	source, err := wakeResolutionMigrationSourceBytes(plan)
	if err != nil {
		return nil, err
	}
	if digestHex(source) != plan.SourceDigest {
		return nil, fmt.Errorf("source digest changed: plan %s current %s", plan.SourceDigest, digestHex(source))
	}
	records, outputs, manifestDigest, err := planWakeResolutionOutputs(source)
	if err != nil {
		return nil, err
	}
	if len(records) != plan.RecordCount {
		return nil, fmt.Errorf("source record count changed: plan %d current %d", plan.RecordCount, len(records))
	}
	if manifestDigest != plan.OutputManifestDigest || !reflect.DeepEqual(outputs, plan.Outputs) {
		return nil, fmt.Errorf("output manifest changed: plan %s current %s", plan.OutputManifestDigest, manifestDigest)
	}

	archivePath := wakeResolutionMigrationArchivePath(plan.HomeDir, plan.SourceDigest)
	if err := writeWakeResolutionArchive(archivePath, source); err != nil {
		return nil, err
	}
	if err := wakeResolutionMigrationCrash("archive"); err != nil {
		return nil, err
	}
	stageDir := wakeResolutionMigrationStageDir(plan.HomeDir, plan.SourceDigest)
	if err := stageWakeResolutionRecords(stageDir, records); err != nil {
		return nil, err
	}
	if err := verifyWakeResolutionDirectoryExact(stageDir, plan.Outputs); err != nil {
		return nil, fmt.Errorf("verify staged wake resolutions: %w", err)
	}
	if err := wakeResolutionMigrationCrash("stage"); err != nil {
		return nil, err
	}
	if err := installWakeResolutionStage(plan.HomeDir, stageDir); err != nil {
		return nil, err
	}
	if err := verifyWakeResolutionDirectoryExact(filepath.Join(plan.HomeDir, wakeResolutionDir), plan.Outputs); err != nil {
		return nil, fmt.Errorf("verify installed wake resolutions: %w", err)
	}
	if err := wakeResolutionMigrationCrash("install"); err != nil {
		return nil, err
	}
	receipt := &WakeResolutionMigrationReceipt{
		HomeDir:              plan.HomeDir,
		HomeIdentity:         plan.HomeIdentity,
		SourcePath:           plan.SourcePath,
		SourceDigest:         plan.SourceDigest,
		ArchivePath:          archivePath,
		RecordCount:          plan.RecordCount,
		OutputManifestDigest: plan.OutputManifestDigest,
		CompletedAt:          time.Now().Unix(),
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0755); err != nil {
		return nil, err
	}
	if err := atomicWrite(receiptPath, append(data, '\n')); err != nil {
		return nil, err
	}
	return receipt, nil
}

func planWakeResolutionOutputs(source []byte) ([]wakeResolutionRecord, []WakeResolutionMigrationOutput, string, error) {
	records, err := parseLegacyWakeResolutionRecords(source)
	if err != nil {
		return nil, nil, "", err
	}
	outputs := make([]WakeResolutionMigrationOutput, 0, len(records))
	seenFiles := make(map[string]string)
	for _, record := range records {
		fileName := resolutionFileName(record.LeaseID, record.EventID)
		data, err := wakeResolutionRecordBytes(record)
		if err != nil {
			return nil, nil, "", err
		}
		if prior, ok := seenFiles[fileName]; ok {
			return nil, nil, "", fmt.Errorf("legacy wake resolution filename collision: %s maps both %s and %s", fileName, prior, record.LeaseID+":"+record.EventID)
		}
		seenFiles[fileName] = record.LeaseID + ":" + record.EventID
		outputs = append(outputs, WakeResolutionMigrationOutput{FileName: fileName, Digest: digestHex(data), LeaseID: record.LeaseID, EventID: record.EventID})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].FileName < outputs[j].FileName })
	manifest, err := json.Marshal(outputs)
	if err != nil {
		return nil, nil, "", err
	}
	return records, outputs, digestHex(manifest), nil
}

func parseLegacyWakeResolutionRecords(source []byte) ([]wakeResolutionRecord, error) {
	lines := strings.Split(string(source), "\n")
	seen := make(map[string]bool)
	var records []wakeResolutionRecord
	for i, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			return nil, fmt.Errorf("invalid legacy wake resolution record on line %d", i+1)
		}
		updatedAt, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || updatedAt <= 0 {
			return nil, fmt.Errorf("invalid legacy wake resolution timestamp on line %d", i+1)
		}
		leaseID := strings.TrimSpace(parts[1])
		eventID := strings.TrimSpace(parts[2])
		summary := strings.TrimSpace(parts[3])
		if leaseID == "" || eventID == "" || summary == "" || !strings.Contains(eventID, ":") {
			return nil, fmt.Errorf("invalid legacy wake resolution fields on line %d", i+1)
		}
		key := leaseID + "\x00" + eventID
		if seen[key] {
			return nil, fmt.Errorf("duplicate legacy wake resolution record on line %d", i+1)
		}
		seen[key] = true
		records = append(records, wakeResolutionRecord{LeaseID: leaseID, EventID: eventID, Summary: summary, State: "completed", UpdatedAt: updatedAt})
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("legacy wake resolution source is empty")
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].LeaseID == records[j].LeaseID {
			return records[i].EventID < records[j].EventID
		}
		return records[i].LeaseID < records[j].LeaseID
	})
	return records, nil
}

func validateWakeResolutionPlanShape(plan *WakeResolutionMigrationPlan) error {
	if plan == nil {
		return fmt.Errorf("wake resolution migration plan is required")
	}
	if plan.SchemaVersion != "munsu.wake-resolution-migration/v1" {
		return fmt.Errorf("unsupported wake resolution migration plan schema %q", plan.SchemaVersion)
	}
	if plan.HomeDir == "" || plan.HomeIdentity == "" || plan.SourcePath == "" || plan.SourceDigest == "" || plan.OutputManifestDigest == "" {
		return fmt.Errorf("incomplete wake resolution migration plan")
	}
	if !isSHA256Hex(plan.SourceDigest) || !isSHA256Hex(plan.OutputManifestDigest) {
		return fmt.Errorf("wake resolution migration plan has invalid digest")
	}
	if plan.RecordCount != len(plan.Outputs) || plan.RecordCount <= 0 {
		return fmt.Errorf("wake resolution migration plan record count mismatch")
	}
	canon, err := canonicalWakeResolutionHome(plan.HomeDir)
	if err != nil {
		return err
	}
	if canon != plan.HomeDir {
		return fmt.Errorf("home identity changed: plan home %q current canonical %q", plan.HomeDir, canon)
	}
	if plan.SourcePath != filepath.Join(plan.HomeDir, wakeResolutionDir) {
		return fmt.Errorf("wake resolution migration plan source path mismatch")
	}
	seen := make(map[string]bool)
	for _, output := range plan.Outputs {
		if output.FileName == "" || output.LeaseID == "" || output.EventID == "" || output.Digest == "" {
			return fmt.Errorf("wake resolution migration plan has incomplete output")
		}
		if strings.ContainsAny(output.FileName, `/\\`) || filepath.Base(output.FileName) != output.FileName || output.FileName != resolutionFileName(output.LeaseID, output.EventID) {
			return fmt.Errorf("wake resolution migration plan has invalid output filename %q", output.FileName)
		}
		if !isSHA256Hex(output.Digest) {
			return fmt.Errorf("wake resolution migration plan has invalid output digest")
		}
		if seen[output.FileName] {
			return fmt.Errorf("wake resolution migration plan has duplicate output filename %q", output.FileName)
		}
		seen[output.FileName] = true
	}
	outputs := append([]WakeResolutionMigrationOutput(nil), plan.Outputs...)
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].FileName < outputs[j].FileName })
	manifest, err := json.Marshal(outputs)
	if err != nil {
		return err
	}
	if digestHex(manifest) != plan.OutputManifestDigest {
		return fmt.Errorf("wake resolution migration plan output manifest digest mismatch")
	}
	return nil
}

func verifyWakeResolutionPlanIdentity(plan *WakeResolutionMigrationPlan) error {
	identity, err := wakeResolutionHomeIdentity(plan.HomeDir)
	if err != nil {
		return err
	}
	if identity != plan.HomeIdentity {
		return fmt.Errorf("home identity changed: plan %q current %q", plan.HomeIdentity, identity)
	}
	return nil
}

func verifyWakeResolutionReceiptMatchesPlan(receipt *WakeResolutionMigrationReceipt, plan *WakeResolutionMigrationPlan) error {
	if receipt.HomeDir != plan.HomeDir || receipt.HomeIdentity != plan.HomeIdentity || receipt.SourcePath != plan.SourcePath || receipt.SourceDigest != plan.SourceDigest || receipt.RecordCount != plan.RecordCount || receipt.OutputManifestDigest != plan.OutputManifestDigest {
		return fmt.Errorf("wake resolution migration receipt does not match plan")
	}
	return nil
}

func verifyWakeResolutionMigrationComplete(plan *WakeResolutionMigrationPlan, receipt *WakeResolutionMigrationReceipt) error {
	if err := verifyWakeResolutionPlanIdentity(plan); err != nil {
		return err
	}
	if receipt.ArchivePath != wakeResolutionMigrationArchivePath(plan.HomeDir, plan.SourceDigest) {
		return fmt.Errorf("wake resolution migration archive path mismatch")
	}
	archive, err := os.ReadFile(receipt.ArchivePath)
	if err != nil {
		return fmt.Errorf("read wake resolution migration archive: %w", err)
	}
	if digestHex(archive) != plan.SourceDigest {
		return fmt.Errorf("wake resolution migration archive digest mismatch")
	}
	records, outputs, manifestDigest, err := planWakeResolutionOutputs(archive)
	if err != nil {
		return err
	}
	if len(records) != plan.RecordCount || manifestDigest != plan.OutputManifestDigest || !reflect.DeepEqual(outputs, plan.Outputs) {
		return fmt.Errorf("wake resolution migration archive does not match plan")
	}
	if err := verifyWakeResolutionDirectoryExact(filepath.Join(plan.HomeDir, wakeResolutionDir), plan.Outputs); err != nil {
		return fmt.Errorf("verify completed wake resolutions: %w", err)
	}
	return nil
}

func wakeResolutionMigrationSourceBytes(plan *WakeResolutionMigrationPlan) ([]byte, error) {
	data, err := os.ReadFile(plan.SourcePath)
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		info, statErr := os.Stat(plan.SourcePath)
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("read legacy wake resolutions: %w", err)
		}
	}
	archivePath := wakeResolutionMigrationArchivePath(plan.HomeDir, plan.SourceDigest)
	data, archiveErr := os.ReadFile(archivePath)
	if archiveErr != nil {
		if os.IsNotExist(archiveErr) {
			return nil, fmt.Errorf("legacy wake resolution source missing and archive not found for digest %s", plan.SourceDigest)
		}
		return nil, fmt.Errorf("read wake resolution migration archive: %w", archiveErr)
	}
	return data, nil
}

func writeWakeResolutionArchive(path string, source []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != string(source) {
			return fmt.Errorf("wake resolution migration archive conflict at %s", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return atomicWrite(path, source)
}

func stageWakeResolutionRecords(stageDir string, records []wakeResolutionRecord) error {
	if err := os.RemoveAll(stageDir); err != nil {
		return err
	}
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		return err
	}
	for _, record := range records {
		data, err := wakeResolutionRecordBytes(record)
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(stageDir, resolutionFileName(record.LeaseID, record.EventID)), data); err != nil {
			return err
		}
	}
	return nil
}

func installWakeResolutionStage(homeDir, stageDir string) error {
	current := filepath.Join(homeDir, wakeResolutionDir)
	if err := os.RemoveAll(current); err != nil {
		return err
	}
	if err := os.MkdirAll(current, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(stageDir, entry.Name()))
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(current, entry.Name()), data); err != nil {
			return err
		}
	}
	return nil
}

func verifyWakeResolutionDirectoryExact(dir string, outputs []WakeResolutionMigrationOutput) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	want := make(map[string]string, len(outputs))
	for _, output := range outputs {
		want[output.FileName] = output.Digest
	}
	gotNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("unexpected directory %s", entry.Name())
		}
		gotNames = append(gotNames, entry.Name())
		wantDigest, ok := want[entry.Name()]
		if !ok {
			return fmt.Errorf("unexpected output file %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		if digestHex(data) != wantDigest {
			return fmt.Errorf("output file %s digest mismatch", entry.Name())
		}
	}
	wantNames := make([]string, 0, len(want))
	for name := range want {
		wantNames = append(wantNames, name)
	}
	sort.Strings(gotNames)
	sort.Strings(wantNames)
	if !reflect.DeepEqual(gotNames, wantNames) {
		return fmt.Errorf("output file set mismatch: got %v want %v", gotNames, wantNames)
	}
	return nil
}

func readWakeResolutionMigrationReceipt(path string) (*WakeResolutionMigrationReceipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var receipt WakeResolutionMigrationReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func wakeResolutionRecordBytes(record wakeResolutionRecord) ([]byte, error) {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func canonicalWakeResolutionHome(homeDir string) (string, error) {
	abs, err := filepath.Abs(homeDir)
	if err != nil {
		return "", err
	}
	canon, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return canon, nil
}

func wakeResolutionHomeIdentity(homeDir string) (string, error) {
	canon, err := canonicalWakeResolutionHome(homeDir)
	if err != nil {
		return "", err
	}
	identity, rank, err := ReadHomeIdentity(canon)
	if err != nil {
		return "", err
	}
	return string(rank) + ":" + identity + ":" + canon, nil
}

func wakeResolutionMigrationReceiptPath(homeDir string) string {
	return filepath.Join(homeDir, wakeResolutionMigrationDir, "receipt.json")
}

func wakeResolutionMigrationArchivePath(homeDir, digest string) string {
	return filepath.Join(homeDir, wakeResolutionMigrationDir, "archive", digest+".legacy")
}

func wakeResolutionMigrationStageDir(homeDir, digest string) string {
	return filepath.Join(homeDir, wakeResolutionMigrationDir, "stage", digest)
}

func resolutionFileName(leaseID, eventID string) string {
	return strings.NewReplacer("/", "_", ":", "_").Replace(leaseID + "-" + eventID + ".json")
}

func digestHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func defaultWakeResolutionPlanOut(homeDir string) string {
	base := filepath.Base(filepath.Clean(homeDir))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "home"
	}
	return filepath.Join(os.TempDir(), "munsu-wake-resolution-"+base+".plan.json")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func wakeResolutionMigrationCrash(phase string) error {
	if wakeResolutionMigrationCrashAfter == phase {
		return fmt.Errorf("injected crash after %s", phase)
	}
	return nil
}
