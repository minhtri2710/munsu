package domain

import (
	"errors"
	"testing"
)

// validMutation returns a fully-resolved mutation using current constants.
func validMutation() Mutation {
	digest, _ := Digest(map[string]string{"op": "bind"})
	return Mutation{
		Home:         HomeID("home-1"),
		Target:       TaskID("gh-1"),
		Operation:    Operation{ID: OperationID("op-1"), Digest: digest},
		Precondition: Of(1, 0),
		FenceToken:   7,
	}
}

func TestMutationValidateAcceptsResolved(t *testing.T) {
	if err := validMutation().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestMutationValidateFailsClosedOnMissingHome(t *testing.T) {
	m := validMutation()
	m.Home = ScopedID{}
	if err := m.Validate(); !errors.Is(err, ErrAuthorityAmbiguous) {
		t.Fatalf("Validate(missing home): got %v, want ErrAuthorityAmbiguous", err)
	}
}

func TestMutationValidateFailsClosedOnMissingTarget(t *testing.T) {
	m := validMutation()
	m.Target = ScopedID{}
	if err := m.Validate(); !errors.Is(err, ErrAuthorityAmbiguous) {
		t.Fatalf("Validate(missing target): got %v, want ErrAuthorityAmbiguous", err)
	}
}

func TestMutationValidateFailsClosedOnMissingFence(t *testing.T) {
	m := validMutation()
	m.FenceToken = 0
	if err := m.Validate(); !errors.Is(err, ErrAuthorityAmbiguous) {
		t.Fatalf("Validate(missing fence): got %v, want ErrAuthorityAmbiguous", err)
	}
}

func TestMutationValidateFailsClosedOnUnresolvedOperation(t *testing.T) {
	m := validMutation()
	m.Operation = Operation{}
	if err := m.Validate(); err == nil {
		t.Fatal("Validate(missing operation) unexpectedly succeeded")
	}
}

func TestMutationValidateFailsClosedOnZeroPrecondition(t *testing.T) {
	m := validMutation()
	m.Precondition = Precondition{}
	if err := m.Validate(); !errors.Is(err, ErrInvalidPrecondition) {
		t.Fatalf("Validate(zero precondition): got %v, want ErrInvalidPrecondition", err)
	}
}

func TestMutationAcceptsCaptainAndResourceTargets(t *testing.T) {
	digest, _ := Digest("x")
	base := validMutation()
	for _, target := range []ScopedID{CaptainID("cap-1"), ResourceID("wrk-1")} {
		m := base
		m.Target = target
		m.Operation = Operation{ID: OperationID("op-x"), Digest: digest}
		if err := m.Validate(); err != nil {
			t.Errorf("Validate(%s): %v", target.Kind, err)
		}
	}
}
