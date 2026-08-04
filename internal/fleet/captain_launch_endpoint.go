package fleet

type LaunchRequest struct{ WindowName, Command, WorkingDir, Backend string }
type LaunchResult struct {
	Backend, Window string
	Meta            map[string]string
}
type LaunchEndpoint interface {
	Launch(home string, req LaunchRequest) (LaunchResult, error)
	Cleanup(home string, result LaunchResult) error
}
