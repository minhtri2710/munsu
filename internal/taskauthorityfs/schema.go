// Package taskauthorityfs defines the versioned on-disk v2 authority schema
// and paths: aggregate revisions, dispatch records, typed audit events, Task
// Operation receipts, and transaction manifests. It is the filesystem adapter
// below taskauthority.Store; documents carry the canonical taskauthority
// records and fail closed on unknown versions or corrupt required fields.
// Legacy v1 task-authority records are detectable here but are never silently
// migrated or mutated; migration is an explicit later slice.
package taskauthorityfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// SchemaVersion is the versioned identity of every v1 authority document. It
// is the single current document identity (ADR-0008 §11), matching the
// canonical taskauthority.TaskAuthoritySchema.
const SchemaVersion = "munsu.task-authority/v1"

// V1SchemaVersion is the legacy v1 aggregate identity. taskauthorityfs
// recognizes it for detection only and never writes or migrates v1 records.
const V1SchemaVersion = "munsu.task-aggregate/v1"

// Sentinel errors reachable through errors.Is on every typed error returned
// by the schema and path layers.
var (
	// ErrUnsupportedVersion means a document's schema version is not the v2
	// authority schema (including legacy v1 records).
	ErrUnsupportedVersion = errors.New("unsupported authority schema version")
	// ErrCorruptDocument means a document failed closed on decode or encode:
	// invalid JSON, a missing required field, or an invalid identity.
	ErrCorruptDocument = errors.New("corrupt authority document")
	// ErrInvalidPath means a path identity could escape or corrupt the
	// authority layout (task IDs, generations, free-form record IDs).
	ErrInvalidPath = errors.New("invalid authority path identity")
)

// UnsupportedVersionError is a typed, inspectable error for documents whose
// schema version this package will not read or write. Kind is "v1" for legacy
// task-authority records (detect-only, never migrated) and "unknown" for any
// other unrecognized version.
type UnsupportedVersionError struct {
	Kind  string
	Found string
	Path  string
}

func (e *UnsupportedVersionError) Error() string {
	if e.Kind == "v1" {
		return fmt.Sprintf("legacy v1 authority document (schema %q) requires explicit migration, never automatic", e.Found)
	}
	return fmt.Sprintf("unsupported authority document schema version %q", e.Found)
}

func (e *UnsupportedVersionError) Unwrap() error { return ErrUnsupportedVersion }

// CorruptDocumentError is a typed, inspectable error for documents that fail
// closed on decode or encode. Document is the record family ("aggregate",
// "hold", "transaction_manifest", ...), Field is the affected JSON field
// (empty when the whole document failed to parse), and Reason is the detail.
type CorruptDocumentError struct {
	Document string
	Field    string
	Reason   string
}

func (e *CorruptDocumentError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("corrupt %s authority document field %q: %s", e.Document, e.Field, e.Reason)
	}
	return fmt.Sprintf("corrupt %s authority document: %s", e.Document, e.Reason)
}

func (e *CorruptDocumentError) Unwrap() error { return ErrCorruptDocument }

// unsupportedVersion builds a typed unsupported-version error, tagging legacy
// v1 documents distinctly from unknown versions.
func unsupportedVersion(found string) error {
	kind := "unknown"
	if found == V1SchemaVersion {
		kind = "v1"
	}
	return &UnsupportedVersionError{Kind: kind, Found: found}
}

// corruptDocument builds a typed corrupt-document error rooted in the
// ErrCorruptDocument sentinel.
func corruptDocument(document, field, format string, args ...any) error {
	return &CorruptDocumentError{
		Document: document,
		Field:    field,
		Reason:   fmt.Sprintf(format, args...),
	}
}

// envelope is the versioned wrapper around every v2 authority document
// payload. Canonical records that carry their own schema identity (aggregate,
// dispatch records) are cross-checked after decode; records without one
// (audit events, receipts, transaction manifests) are versioned solely by the
// envelope. The envelope is decoded first so unknown versions fail closed
// before payload decode.
type envelope struct {
	SchemaVersion string          `json:"schema_version"`
	Record        json.RawMessage `json:"record"`
}

// versionedDecode validates the document envelope, then decodes and validates
// the payload. Unsupported versions and envelopes without a required record
// fail closed before payload decode.
func versionedDecode(document string, data []byte, payload any, validate func() error) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return corruptDocument(document, "", "invalid JSON: %v", err)
	}
	if env.SchemaVersion == "" {
		return corruptDocument(document, "schema_version", "missing required schema_version")
	}
	if env.SchemaVersion != SchemaVersion {
		return unsupportedVersion(env.SchemaVersion)
	}
	if len(env.Record) == 0 {
		return corruptDocument(document, "record", "missing required record payload")
	}
	if err := json.Unmarshal(env.Record, payload); err != nil {
		return corruptDocument(document, "record", "invalid JSON: %v", err)
	}
	if err := validate(); err != nil {
		return err
	}
	return nil
}

// versionedEncode validates a record and renders its deterministic v2
// envelope document, so invalid records fail closed before serialization.
func versionedEncode(document string, record any, validate func() error) ([]byte, error) {
	if err := validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, corruptDocument(document, "record", "encoding: %v", err)
	}
	data, err := json.MarshalIndent(envelope{SchemaVersion: SchemaVersion, Record: payload}, "", "  ")
	if err != nil {
		return nil, corruptDocument(document, "", "encoding: %v", err)
	}
	return append(data, '\n'), nil
}

// DigestHex returns the sha256 hex digest used for manifest before/after
// entry verification.
func DigestHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// validDigest reports whether s is exactly one 64-character hexadecimal
// SHA-256 digest. Length alone is not sufficient: non-hex content of the
// right length must also fail closed.
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// validateRecordVersion rejects a canonical record whose own schema identity
// disagrees with the v2 document schema.
func validateRecordVersion(recordVersion string) error {
	if recordVersion != SchemaVersion {
		return unsupportedVersion(recordVersion)
	}
	return nil
}

// EncodeAggregate renders the deterministic v2 JSON document for one
// authoritative aggregate record.
func EncodeAggregate(agg taskauthority.Aggregate) ([]byte, error) {
	return versionedEncode("aggregate", agg, func() error {
		if err := validateRecordVersion(agg.SchemaVersion); err != nil {
			return err
		}
		return validateAggregateDocument(agg)
	})
}

// DecodeAggregate parses and validates one v2 aggregate document, failing
// closed on unknown schema versions, legacy v1 records, and corrupt required
// fields.
func DecodeAggregate(data []byte) (taskauthority.Aggregate, error) {
	var agg taskauthority.Aggregate
	err := versionedDecode("aggregate", data, &agg, func() error {
		return validateAggregateDocument(agg)
	})
	return agg, err
}

// validateAggregateDocument checks the document-shape fields of an aggregate:
// schema identity, task identity, generation, revision, phase, and owner.
// Binding and lifecycle semantics remain with taskauthority.
func validateAggregateDocument(agg taskauthority.Aggregate) error {
	if err := validateRecordVersion(agg.SchemaVersion); err != nil {
		return err
	}
	if err := validateTaskID(agg.TaskID); err != nil {
		return corruptDocument("aggregate", "task_id", "invalid task id %q", agg.TaskID)
	}
	if err := agg.Generation.Validate(); err != nil {
		return corruptDocument("aggregate", "generation", "%v", err)
	}
	if agg.Revision == 0 {
		return corruptDocument("aggregate", "revision", "aggregate revision must be positive")
	}
	if !agg.Phase.Valid() {
		return corruptDocument("aggregate", "phase", "invalid phase %q", agg.Phase)
	}
	if strings.TrimSpace(agg.Definition.Owner) == "" {
		return corruptDocument("aggregate", "owner", "aggregate requires an owner")
	}
	return nil
}

// EncodeHold renders the deterministic v2 JSON document for one dispatch hold.
func EncodeHold(hold taskauthority.DispatchHold) ([]byte, error) {
	return versionedEncode("hold", hold, func() error {
		if err := validateRecordVersion(hold.SchemaVersion); err != nil {
			return err
		}
		return validateHoldDocument(hold)
	})
}

// DecodeHold parses and validates one v2 dispatch hold document.
func DecodeHold(data []byte) (taskauthority.DispatchHold, error) {
	var hold taskauthority.DispatchHold
	err := versionedDecode("hold", data, &hold, func() error {
		return validateHoldDocument(hold)
	})
	return hold, err
}

// validateHoldDocument checks the identity and required fields of a dispatch
// hold document: schema identity, id, actions, reason, and creation time.
func validateHoldDocument(hold taskauthority.DispatchHold) error {
	if err := validateRecordVersion(hold.SchemaVersion); err != nil {
		return err
	}
	if hold.ID == "" || strings.ContainsAny(hold.ID, `/\\`) {
		return corruptDocument("hold", "id", "invalid dispatch hold id %q", hold.ID)
	}
	if len(hold.Actions) == 0 {
		return corruptDocument("hold", "actions", "dispatch hold requires at least one action")
	}
	for _, action := range hold.Actions {
		if !action.Valid() {
			return corruptDocument("hold", "actions", "unknown dispatch action %q", action)
		}
	}
	if strings.TrimSpace(hold.Reason) == "" {
		return corruptDocument("hold", "reason", "dispatch hold requires a reason")
	}
	if hold.CreatedAt <= 0 {
		return corruptDocument("hold", "created_at", "dispatch hold requires a created timestamp")
	}
	return nil
}

// EncodeInterpretation renders the deterministic v2 JSON document for one
// dispatch interpretation record.
func EncodeInterpretation(rec taskauthority.DispatchInterpretation) ([]byte, error) {
	return versionedEncode("interpretation", rec, func() error {
		if err := validateRecordVersion(rec.SchemaVersion); err != nil {
			return err
		}
		return validateInterpretationDocument(rec)
	})
}

// DecodeInterpretation parses and validates one v2 dispatch interpretation
// document.
func DecodeInterpretation(data []byte) (taskauthority.DispatchInterpretation, error) {
	var rec taskauthority.DispatchInterpretation
	err := versionedDecode("interpretation", data, &rec, func() error {
		return validateInterpretationDocument(rec)
	})
	return rec, err
}

// validateInterpretationDocument checks the identity and required fields of a
// dispatch interpretation document.
func validateInterpretationDocument(rec taskauthority.DispatchInterpretation) error {
	if err := validateRecordVersion(rec.SchemaVersion); err != nil {
		return err
	}
	if rec.ID == "" || strings.ContainsAny(rec.ID, `/\\`) {
		return corruptDocument("interpretation", "id", "invalid interpretation id %q", rec.ID)
	}
	if rec.CreatedAt <= 0 {
		return corruptDocument("interpretation", "created_at", "interpretation requires a created timestamp")
	}
	return nil
}

// EncodeDecision renders the deterministic v2 JSON document for one dispatch
// decision.
func EncodeDecision(dec taskauthority.DispatchDecision) ([]byte, error) {
	return versionedEncode("decision", dec, func() error {
		if err := validateRecordVersion(dec.SchemaVersion); err != nil {
			return err
		}
		return validateDecisionDocument(dec)
	})
}

// DecodeDecision parses and validates one v2 dispatch decision document.
func DecodeDecision(data []byte) (taskauthority.DispatchDecision, error) {
	var dec taskauthority.DispatchDecision
	err := versionedDecode("decision", data, &dec, func() error {
		return validateDecisionDocument(dec)
	})
	return dec, err
}

// validateDecisionDocument checks the identity and required fields of a
// dispatch decision document.
func validateDecisionDocument(dec taskauthority.DispatchDecision) error {
	if err := validateRecordVersion(dec.SchemaVersion); err != nil {
		return err
	}
	if dec.Key == "" {
		return corruptDocument("decision", "key", "dispatch decision requires a key")
	}
	if dec.InterpretationID == "" {
		return corruptDocument("decision", "interpretation_id", "dispatch decision requires an interpretation id")
	}
	if dec.CreatedAt <= 0 {
		return corruptDocument("decision", "created_at", "dispatch decision requires a created timestamp")
	}
	return nil
}

// EncodeLeaseMarker renders the worktree lease marker document in the
// home-compatible bare format read by internal/home.TaskWorktreeLeaseActive.
// The marker is a compatibility artifact committed atomically with the
// worktree binding it accompanies; it carries no versioned envelope so the
// legacy read path parses it unchanged.
func EncodeLeaseMarker(marker taskauthority.LeaseMarker) ([]byte, error) {
	if err := marker.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, corruptDocument("lease_marker", "", "encoding: %v", err)
	}
	return append(data, '\n'), nil
}

// EncodeAudit renders the deterministic v2 JSON document for one typed audit
// event.
func EncodeAudit(ev taskauthority.AuditEvent) ([]byte, error) {
	return versionedEncode("audit", ev, func() error {
		return validateAuditDocument(ev)
	})
}

// DecodeAudit parses and validates one v2 typed audit event document.
func DecodeAudit(data []byte) (taskauthority.AuditEvent, error) {
	var ev taskauthority.AuditEvent
	err := versionedDecode("audit", data, &ev, func() error {
		return validateAuditDocument(ev)
	})
	return ev, err
}

// validateAuditDocument delegates to the canonical taskauthority audit event
// validation, which owns typed event shape and phases.
func validateAuditDocument(ev taskauthority.AuditEvent) error {
	if err := ev.Validate(); err != nil {
		return corruptDocument("audit", "", "%v", err)
	}
	return nil
}

// EncodeReceipt renders the deterministic v2 JSON document for one committed
// Task Operation receipt. Replayed is computed metadata and is never
// persisted.
func EncodeReceipt(receipt taskauthority.Receipt) ([]byte, error) {
	return versionedEncode("receipt", receipt, func() error {
		return validateReceiptDocument(receipt)
	})
}

// DecodeReceipt parses and validates one v2 Task Operation receipt document.
func DecodeReceipt(data []byte) (taskauthority.Receipt, error) {
	var receipt taskauthority.Receipt
	err := versionedDecode("receipt", data, &receipt, func() error {
		return validateReceiptDocument(receipt)
	})
	return receipt, err
}

// validateReceiptDocument checks the identity and required fields of a Task
// Operation receipt document: a safe operation id, an exactly-64-hex intent
// digest, a committed timestamp, and, when a lifecycle outcome is present, a
// coherent all-or-none task id/generation/revision/valid phase set. Receipts
// without a lifecycle outcome (e.g. hold-only operations) remain valid.
func validateReceiptDocument(receipt taskauthority.Receipt) error {
	if receipt.OperationID == "" || strings.ContainsAny(receipt.OperationID, `/\\`) {
		return corruptDocument("receipt", "operation_id", "receipt requires a safe operation id")
	}
	if !validDigest(receipt.Digest) {
		return corruptDocument("receipt", "digest", "receipt digest must be a 64-hex sha256 digest")
	}
	if receipt.CommittedAt <= 0 {
		return corruptDocument("receipt", "committed_at", "receipt requires a committed timestamp")
	}
	if hasLifecycleOutcome(receipt) {
		if err := validateTaskID(receipt.TaskID); err != nil {
			return corruptDocument("receipt", "task_id", "receipt lifecycle outcome requires a valid task id")
		}
		if err := receipt.Generation.Validate(); err != nil {
			return corruptDocument("receipt", "generation", "receipt lifecycle outcome requires a valid generation")
		}
		if receipt.Revision == 0 {
			return corruptDocument("receipt", "revision", "receipt lifecycle outcome requires a positive revision")
		}
		if !receipt.Phase.Valid() {
			return corruptDocument("receipt", "phase", "receipt lifecycle outcome requires a valid phase, got %q", receipt.Phase)
		}
	}
	return nil
}

// hasLifecycleOutcome reports whether a receipt carries any lifecycle outcome
// field: task identity, generation, revision, phase, or the reopened flag.
// Presence of any of them requires the full coherent set.
func hasLifecycleOutcome(receipt taskauthority.Receipt) bool {
	return receipt.TaskID != "" || receipt.Generation != 0 || receipt.Revision != 0 || receipt.Phase != "" || receipt.Reopened
}

// ManifestState is the commit state of one transaction manifest.
type ManifestState string

const (
	// ManifestPending means the manifest is written but the transaction has
	// not committed; recovery may apply or discard it.
	ManifestPending ManifestState = "pending"
	// ManifestCommitted means the transaction committed; recovery finishes
	// idempotently from the commit marker.
	ManifestCommitted ManifestState = "committed"
)

// ManifestEntry is one file mutation in a transaction manifest. Before
// entries carry only the digest of the existing file; after entries carry the
// payload to apply plus its digest.
type ManifestEntry struct {
	Path    string `json:"path"`
	Digest  string `json:"digest,omitempty"`
	Payload string `json:"payload,omitempty"`
}

// TransactionManifest is the immutable write-ahead record of one Store
// update. It pins the Task Operation identity, request digest, expected Task
// Generation, before digests, after payloads, typed audit event, and commit
// state so recovery is deterministic at every journal stage. Its version
// identity lives in the document envelope.
type TransactionManifest struct {
	OperationID        string                    `json:"operation_id"`
	Digest             string                    `json:"digest"`
	ExpectedGeneration taskauthority.Generation  `json:"expected_generation,omitempty"`
	Before             []ManifestEntry           `json:"before,omitempty"`
	After              []ManifestEntry           `json:"after"`
	Audit              *taskauthority.AuditEvent `json:"audit,omitempty"`
	State              ManifestState             `json:"state"`
	CreatedAt          int64                     `json:"created_at"`
}

// EncodeTransactionManifest renders the deterministic v2 JSON document for
// one transaction manifest.
func EncodeTransactionManifest(manifest TransactionManifest) ([]byte, error) {
	return versionedEncode("transaction_manifest", manifest, func() error {
		return validateTransactionManifest(manifest)
	})
}

// DecodeTransactionManifest parses and validates one v2 transaction manifest
// document.
func DecodeTransactionManifest(data []byte) (TransactionManifest, error) {
	var manifest TransactionManifest
	err := versionedDecode("transaction_manifest", data, &manifest, func() error {
		return validateTransactionManifest(manifest)
	})
	return manifest, err
}

// validateTransactionManifest checks the identity, required fields, digest
// consistency, manifest entry paths, and commit state of a transaction
// manifest.
func validateTransactionManifest(manifest TransactionManifest) error {
	if manifest.OperationID == "" || strings.ContainsAny(manifest.OperationID, `/\\`) {
		return corruptDocument("transaction_manifest", "operation_id", "manifest requires a safe operation id")
	}
	if !validDigest(manifest.Digest) {
		return corruptDocument("transaction_manifest", "digest", "manifest digest must be a 64-hex sha256 digest")
	}
	if manifest.ExpectedGeneration != 0 {
		if err := manifest.ExpectedGeneration.Validate(); err != nil {
			return corruptDocument("transaction_manifest", "expected_generation", "%v", err)
		}
	}
	if len(manifest.After) == 0 {
		return corruptDocument("transaction_manifest", "after", "manifest requires at least one after entry")
	}
	if err := validateManifestEntryPaths("after", manifest.After); err != nil {
		return err
	}
	afterPaths := make(map[string]bool, len(manifest.After))
	for _, entry := range manifest.After {
		afterPaths[entry.Path] = true
		if !validDigest(entry.Digest) {
			return corruptDocument("transaction_manifest", "after", "after entry %q digest must be a 64-hex sha256 digest", entry.Path)
		}
		if DigestHex([]byte(entry.Payload)) != entry.Digest {
			return corruptDocument("transaction_manifest", "after", "after entry %q payload does not match its digest", entry.Path)
		}
	}
	if err := validateManifestEntryPaths("before", manifest.Before); err != nil {
		return err
	}
	for _, entry := range manifest.Before {
		if !validDigest(entry.Digest) {
			return corruptDocument("transaction_manifest", "before", "before entry %q digest must be a 64-hex sha256 digest", entry.Path)
		}
		if !afterPaths[entry.Path] {
			return corruptDocument("transaction_manifest", "before", "before entry %q has no matching after entry; v2 transactions do not represent deletion", entry.Path)
		}
	}
	if manifest.Audit != nil {
		if err := manifest.Audit.Validate(); err != nil {
			return corruptDocument("transaction_manifest", "audit", "%v", err)
		}
		if manifest.Audit.OperationID != manifest.OperationID {
			return corruptDocument("transaction_manifest", "audit", "audit operation id %q does not match manifest operation id %q", manifest.Audit.OperationID, manifest.OperationID)
		}
	}
	switch manifest.State {
	case ManifestPending, ManifestCommitted:
	default:
		return corruptDocument("transaction_manifest", "state", "unknown commit state %q", manifest.State)
	}
	if manifest.CreatedAt <= 0 {
		return corruptDocument("transaction_manifest", "created_at", "manifest requires a created timestamp")
	}
	return nil
}

// validateManifestEntryPaths checks every entry path of one manifest list:
// each path must be relative, clean, traversal-free, and constrained under the
// v2 authority namespace, and no path may repeat within the list. The same
// path may appear once in each of before and after, which is the update shape.
func validateManifestEntryPaths(kind string, entries []ManifestEntry) error {
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !manifestPathValid(entry.Path) {
			return corruptDocument("transaction_manifest", kind, "%s entry path %q must be a clean relative path under %s", kind, entry.Path, authorityRoot)
		}
		if seen[entry.Path] {
			return corruptDocument("transaction_manifest", kind, "%s entry path %q appears more than once", kind, entry.Path)
		}
		seen[entry.Path] = true
	}
	return nil
}

// manifestPathValid reports whether p is a relative, clean, traversal-free
// path constrained strictly under the v2 authority namespace. Both separator
// styles are treated as separators so manifests written on any platform
// validate identically.
func manifestPathValid(p string) bool {
	if p == "" || filepath.IsAbs(p) {
		return false
	}
	if filepath.Clean(p) != p {
		return false
	}
	slash := strings.ReplaceAll(p, "\\", "/")
	if slash == "." || slash == ".." || strings.HasPrefix(slash, "../") || strings.HasSuffix(slash, "/..") || strings.Contains(slash, "/../") {
		return false
	}
	return strings.HasPrefix(slash, authorityRoot+"/")
}
