package captain

type LaunchRequest struct{ WindowName, Command, WorkingDir string }
type LaunchResult struct {
	Backend, Window string
	Meta            map[string]string
}
type LaunchEndpoint interface {
	Launch(home string, req LaunchRequest) (LaunchResult, error)
	Cleanup(home string, result LaunchResult) error
}
