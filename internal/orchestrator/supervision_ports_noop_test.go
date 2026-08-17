package orchestrator

import "fmt"

// The three stubs below satisfy the watcher's capability ports with the
// "capability unavailable" behaviour every test that does not exercise a real
// port wants. They live here rather than beside the interfaces because nothing
// in the binary ever constructs one: production always supplies a real port,
// and a stub that only tests build belongs in the test binary.

type NoopTaskStatePort struct{}

func (NoopTaskStatePort) ReadTaskState(string, string) (*ObservedTaskState, error) {
	return nil, fmt.Errorf("task state capability unavailable")
}

type NoopRetirementPort struct{}

func (NoopRetirementPort) RecoverPendingRetirements(string) (int, []error) { return 0, nil }
func (NoopRetirementPort) RetireMergedPoll(string, string, string) error {
	return fmt.Errorf("retirement capability unavailable")
}

type NoopWatcherHooks struct{}

func (NoopWatcherHooks) Reconcile(string, bool) error { return nil }
func (NoopWatcherHooks) Activate(string)              {}
