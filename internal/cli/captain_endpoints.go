package cli

import (
	"errors"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/session"
)

type sessionLaunchEndpoint struct {
	resolve func(string, string) (session.Backend, string, error)
}

func newSessionLaunchEndpoint() sessionLaunchEndpoint {
	return sessionLaunchEndpoint{resolve: session.Resolve}
}
func (e sessionLaunchEndpoint) Launch(home string, req captain.LaunchRequest) (captain.LaunchResult, error) {
	bk, name, err := e.resolve(home, "")
	if err != nil {
		return captain.LaunchResult{}, err
	}
	if herdr, ok := bk.(*session.HerdrBackend); ok {
		herdr.Cwd = req.WorkingDir
	}
	window, err := bk.NewWindow(req.ContainerLabel, req.WindowName)
	if err != nil {
		return captain.LaunchResult{}, err
	}
	if err := bk.SendKeys(window, req.Command); err != nil {
		_ = bk.Teardown(window)
		return captain.LaunchResult{}, err
	}
	meta := map[string]string{}
	if extras, ok := bk.(session.BackendMetaExtras); ok {
		for k, v := range extras.MetaExtras() {
			meta[k] = v
		}
	}
	return captain.LaunchResult{Backend: name, Window: window, Meta: meta}, nil
}
func (e sessionLaunchEndpoint) Cleanup(home string, result captain.LaunchResult) error {
	bk, _, err := e.resolve(home, "")
	if err != nil {
		return err
	}
	return bk.Teardown(result.Window)
}

type sessionProbeEndpoint struct {
	resolve func(string, map[string]string) (session.Backend, string, error)
}

func newSessionProbeEndpoint() sessionProbeEndpoint {
	return sessionProbeEndpoint{resolve: session.BackendForTask}
}

func (e sessionProbeEndpoint) Probe(home string, meta map[string]string) (captain.ProbeResult, error) {
	bk, name, err := e.resolve(home, meta)
	if err != nil {
		return captain.ProbeResult{}, err
	}
	if expected := meta["backend"]; expected != "" && name != expected {
		return captain.ProbeResult{}, errors.New("captain backend ownership mismatch")
	}
	if aware, ok := bk.(session.AgentAwareBackend); ok {
		pane, agent, err := aware.CheckAgentAlive(meta["window"])
		if errors.Is(err, session.ErrPaneNotFound) {
			return captain.ProbeResult{}, nil
		}
		return captain.ProbeResult{PaneAlive: pane, AgentAlive: agent}, err
	}
	alive := bk.Alive(meta["window"])
	return captain.ProbeResult{PaneAlive: alive, AgentAlive: alive}, nil
}
