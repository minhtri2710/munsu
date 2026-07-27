package cli

import (
	"errors"
	"time"

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

func ownedCaptainBackend(home string, meta map[string]string, resolve func(string, map[string]string) (session.Backend, string, error)) (session.Backend, error) {
	bk, name, err := resolve(home, meta)
	if err != nil {
		return nil, err
	}
	if expected := meta["backend"]; expected != "" && name != expected {
		return nil, errors.New("captain backend ownership mismatch")
	}
	if name == "herdr" && meta["herdr_session"] != "" {
		sessionID, _ := session.ParseWindow(meta["window"])
		if sessionID != "" && sessionID != meta["herdr_session"] {
			return nil, errors.New("herdr session ownership mismatch")
		}
	}
	return bk, nil
}

func probeCaptainBackend(bk session.Backend, window string) (captain.ProbeResult, error) {
	if aware, ok := bk.(session.AgentAwareBackend); ok {
		pane, agent, err := aware.CheckAgentAlive(window)
		if errors.Is(err, session.ErrPaneNotFound) {
			return captain.ProbeResult{}, nil
		}
		return captain.ProbeResult{PaneAlive: pane, AgentAlive: agent}, err
	}
	alive := bk.Alive(window)
	return captain.ProbeResult{PaneAlive: alive, AgentAlive: alive}, nil
}

func (e sessionProbeEndpoint) Probe(home string, meta map[string]string) (captain.ProbeResult, error) {
	bk, err := ownedCaptainBackend(home, meta, e.resolve)
	if err != nil {
		return captain.ProbeResult{}, err
	}
	return probeCaptainBackend(bk, meta["window"])
}

type sessionNudgeEndpoint struct {
	resolve func(string, map[string]string) (session.Backend, string, error)
}

func newSessionNudgeEndpoint() sessionNudgeEndpoint {
	return sessionNudgeEndpoint{resolve: session.BackendForTask}
}

func (e sessionNudgeEndpoint) Nudge(home string, meta map[string]string, payload string) (captain.NudgeResult, error) {
	bk, err := ownedCaptainBackend(home, meta, e.resolve)
	if err != nil {
		return captain.NudgeResult{}, err
	}
	result, err := probeCaptainBackend(bk, meta["window"])
	if err != nil {
		return captain.NudgeResult{}, err
	}
	if !result.PaneAlive || !result.AgentAlive {
		return captain.NudgeResult{Status: "unavailable"}, nil
	}
	prompt := session.SubmitPrompt(bk, meta["window"], payload)
	return captain.NudgeResult{Status: string(prompt.Status), Detail: prompt.Detail, Acknowledged: prompt.Acknowledged()}, prompt.Err
}

type sessionRetireEndpoint struct {
	resolve func(string, map[string]string) (session.Backend, string, error)
	sleep   func(time.Duration)
}

func newSessionRetireEndpoint() sessionRetireEndpoint {
	return sessionRetireEndpoint{resolve: session.BackendForTask, sleep: time.Sleep}
}

func (e sessionRetireEndpoint) Retire(home string, meta map[string]string) error {
	bk, err := ownedCaptainBackend(home, meta, e.resolve)
	if err != nil {
		return err
	}
	window := meta["window"]
	if bk.Alive(window) {
		if err := bk.SendKeys(window, "/quit"); err != nil {
			return err
		}
		if e.sleep != nil {
			e.sleep(500 * time.Millisecond)
		}
		if !bk.Alive(window) {
			return nil
		}
	}
	return bk.Teardown(window)
}
