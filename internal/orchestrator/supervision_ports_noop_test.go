package orchestrator

// The stubs below satisfy the watcher's capability ports for every test that
// does not exercise a real port. They live here rather than beside the
// interfaces because nothing in the binary ever constructs one: production
// always supplies a real port, and a stub that only tests build belongs in the
// test binary.

// NoopActiveDeliveryPort reports no active delivery journals.
type NoopActiveDeliveryPort struct{}

func (NoopActiveDeliveryPort) ActiveDeliveries(string) ([]ActiveDelivery, error) { return nil, nil }

type NoopWatcherHooks struct{}

func (NoopWatcherHooks) Reconcile(string, bool) error { return nil }
func (NoopWatcherHooks) Activate(string)              {}
