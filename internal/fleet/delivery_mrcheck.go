package fleet

import (
	"fmt"
)

// This file retains the provider-neutral merge-status read seam (MergeStatus
// and its typed error) used by watcher .check scripts. The pr-check routing
// (RoutePRCheck / PRCheck / MRLiveCheck) and its delivery preparation were
// removed with the legacy delivery path (#414 B); delivery execution now
// runs exclusively through the journaled Deliver operation. No delivery
// mutation semantics remain here.

// MergeStatusError classifies a non-merged delivery status.
type MergeStatusError struct {
	Unverifiable bool
	Err          error
}

func (e *MergeStatusError) Error() string { return e.Err.Error() }
func (e *MergeStatusError) Unwrap() error { return e.Err }

// MergeStatus reports the merge status for a task with a delivery identity.
// Exit status: 0 = merged, 1 = not merged, 2 = error/unverifiable.
// Used as the provider-neutral watcher seam invoked by .check scripts.
func MergeStatus(homeDir, id string) error {
	ident, err := RequireIdentity(homeDir, id)
	if err != nil {
		return &MergeStatusError{Unverifiable: true, Err: fmt.Errorf("cannot read delivery identity: %w", err)}
	}

	status, err := QueryDeliveryMergeStatus(ident)
	if err != nil {
		return &MergeStatusError{Unverifiable: true, Err: fmt.Errorf("merge status query: %w", err)}
	}

	if status.Merged {
		fmt.Printf("%s/%s#%d merged (state=%s, head=%s)\n",
			ident.Owner, ident.Repo, ident.Number, status.State, status.HeadSHA)
		return nil
	}

	if status.Closed {
		fmt.Printf("%s/%s#%d closed (not merged, state=%s)\n",
			ident.Owner, ident.Repo, ident.Number, status.State)
		return &MergeStatusError{Err: fmt.Errorf("MR %d is closed but not merged (state=%s)", ident.Number, status.State)}
	}

	fmt.Printf("%s/%s#%d open (not merged, state=%s)\n",
		ident.Owner, ident.Repo, ident.Number, status.State)
	return &MergeStatusError{Err: fmt.Errorf("MR %d is open and not merged (state=%s)", ident.Number, status.State)}
}
