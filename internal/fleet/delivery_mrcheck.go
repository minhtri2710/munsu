package fleet

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file retains the provider-neutral merge-status read seam (MergeStatus
// and its typed error) behind munsu delivery merge-status. No delivery
// mutation semantics remain here.

// MergeStatusError classifies a non-merged delivery status.
type MergeStatusError struct {
	Unverifiable bool
	Err          error
}

func (e *MergeStatusError) Error() string { return e.Err.Error() }
func (e *MergeStatusError) Unwrap() error { return e.Err }

// MergeStatus reports whether the task's delivery landed, through
// classifyLanding. Exit status: 0 = merged, 1 = not merged, 2 =
// error/unverifiable.
func MergeStatus(homeDir, id string) error {
	ident, forge, err := deliveryTarget(homeDir, id)
	if err != nil {
		return &MergeStatusError{Unverifiable: true, Err: fmt.Errorf("cannot read delivery identity: %w", err)}
	}
	status, err := QueryDeliveryMergeStatus(forge, ident)
	if err != nil {
		return &MergeStatusError{Unverifiable: true, Err: fmt.Errorf("merge status query: %w", err)}
	}

	landed, err := classifyLanding(*status, *ident)
	switch landed {
	case landingLanded:
		fmt.Printf("%s/%s#%d merged (state=%s, head=%s)\n",
			ident.Owner, ident.Repo, ident.Number, status.State, status.HeadSHA)
		return nil
	case landingClosed:
		fmt.Printf("%s/%s#%d closed (not merged, state=%s)\n",
			ident.Owner, ident.Repo, ident.Number, status.State)
		return &MergeStatusError{Err: fmt.Errorf("MR %d is closed but not merged (state=%s)", ident.Number, status.State)}
	case landingOpen:
		fmt.Printf("%s/%s#%d open (not merged, state=%s)\n",
			ident.Owner, ident.Repo, ident.Number, status.State)
		return &MergeStatusError{Err: fmt.Errorf("MR %d is open and not merged (state=%s)", ident.Number, status.State)}
	}
	return &MergeStatusError{Unverifiable: true, Err: fmt.Errorf("merge status query: %w", err)}
}

// deliveryTarget reads the identity of the task's current delivery
// authorization and the forge step its contract captured. The authorization
// commits before the provider merge, so the identity is readable while a
// delivery is in flight.
func deliveryTarget(homeDir, id string) (*domain.DeliveryIdentity, taskauthority.DeliveryStep, error) {
	c, err := canonicalAuthority(homeDir)
	if err != nil {
		return nil, taskauthority.DeliveryStep{}, err
	}
	forge, err := capturedForgeStep(c, id)
	if err != nil {
		return nil, taskauthority.DeliveryStep{}, err
	}
	ident, err := deliveryIdentity(c, id)
	if err != nil {
		return nil, taskauthority.DeliveryStep{}, err
	}
	if ident == nil {
		return nil, taskauthority.DeliveryStep{}, fmt.Errorf("task %s has no delivery authorization; run `munsu delivery pr-merge <task-id> <pr-url>` to deliver it", id)
	}
	return ident, forge, nil
}

// deliveryIdentity is the one owner of a task's delivery identity: the
// identity of its current generation's delivery authorization, nil when it has
// none. An authorization a reopened task's earlier generation issued is not
// this generation's delivery.
func deliveryIdentity(c *taskauthority.Canonical, id string) (*domain.DeliveryIdentity, error) {
	tid, err := domain.NewTaskID(id)
	if err != nil {
		return nil, err
	}
	cur, err := c.DeliveryCurrency(tid)
	if err != nil {
		return nil, err
	}
	if cur.Authorization == nil || cur.Authorization.Generation != cur.Generation {
		return nil, nil
	}
	return cur.Identity, nil
}
