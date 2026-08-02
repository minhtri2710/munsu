package taskauthority

// TransferRequest is the immutable request payload of one cross-home task
// transfer (ADR-0007 §10). Its digest binds the transfer intent; Operation
// IDs are excluded so a retry that changes the request under the same IDs
// detects a conflict.
type TransferRequest struct {
	SourceHome      string     `json:"source_home"`
	DestinationHome string     `json:"destination_home"`
	TaskID          string     `json:"task_id"`
	Generation      Generation `json:"generation"`
}

// Digest returns the deterministic sha256 request digest binding the
// transfer request.
func (r TransferRequest) Digest() (string, error) {
	return requestDigest(r)
}

// TransferIntent is the durable binding of one cross-home task transfer:
// source and destination home identity, the exact Task Generation, the
// request digest, and the stable Task Operation identities on the source and
// destination sides (ADR-0007 §10). Retry reuses the same intent and
// Operation IDs; the destination receipt replays or conflicts on the request
// digest.
type TransferIntent struct {
	SourceHome             string     `json:"source_home"`
	DestinationHome        string     `json:"destination_home"`
	TaskID                 string     `json:"task_id"`
	Generation             Generation `json:"generation"`
	RequestDigest          string     `json:"request_digest"`
	SourceOperationID      string     `json:"source_operation_id"`
	DestinationOperationID string     `json:"destination_operation_id"`
}

// Validate rejects empty or mismatched home identities, unsafe task IDs,
// invalid Generations, malformed request digests, and unsafe Operation IDs.
func (ti TransferIntent) Validate() error {
	if ti.SourceHome == "" || ti.DestinationHome == "" {
		return validationError("transfer intent missing home identity")
	}
	if ti.SourceHome == ti.DestinationHome {
		return validationError("transfer intent source and destination are the same home")
	}
	if err := validateTaskID(ti.TaskID); err != nil {
		return err
	}
	if err := ti.Generation.Validate(); err != nil {
		return err
	}
	if !isSHA256Hex(ti.RequestDigest) {
		return validationError("transfer intent request digest must be a 64-hex sha256 digest")
	}
	if err := validateOperationID(ti.SourceOperationID); err != nil {
		return err
	}
	if err := validateOperationID(ti.DestinationOperationID); err != nil {
		return err
	}
	return nil
}
