package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
)

type sessionLaunchEndpoint struct {
	resolve func(string, string) (backend.Backend, string, error)
}

func newSessionLaunchEndpoint() sessionLaunchEndpoint {
	return sessionLaunchEndpoint{resolve: backend.Resolve}
}
func (e sessionLaunchEndpoint) Launch(home string, req fleet.LaunchRequest) (fleet.LaunchResult, error) {
	// The backend identity is resolved by the caller (fleet.Launch) from the
	// captain's published snapshot; the endpoint never hardcodes "" and never
	// auto-detects. An empty identity is a caller/contract violation.
	if req.Backend == "" {
		return fleet.LaunchResult{}, fmt.Errorf("captain launch requires an explicit backend identity (resolved snapshot Backend)")
	}
	bk, name, err := e.resolve(home, req.Backend)
	if err != nil {
		return fleet.LaunchResult{}, err
	}
	if herdr, ok := bk.(*backend.HerdrBackend); ok {
		herdr.Cwd = req.WorkingDir
	}
	window, err := bk.NewWindow(backend.WorkspaceTag(req.WorkingDir), req.WindowName)
	if err != nil {
		return fleet.LaunchResult{}, err
	}
	if err := bk.SendKeys(window, req.Command); err != nil {
		_ = bk.Teardown(window)
		return fleet.LaunchResult{}, err
	}
	meta := map[string]string{}
	if extras, ok := bk.(backend.BackendMetaExtras); ok {
		for k, v := range extras.MetaExtras() {
			meta[k] = v
		}
	}
	return fleet.LaunchResult{Backend: name, Window: window, Meta: meta}, nil
}
func (e sessionLaunchEndpoint) Cleanup(home string, result fleet.LaunchResult) error {
	// Post-launch lifecycle consumes the durable bound identity from the
	// launch result (the same identity used at creation).
	if result.Backend == "" {
		return fmt.Errorf("captain cleanup requires the bound backend identity (launch result Backend)")
	}
	bk, _, err := e.resolve(home, result.Backend)
	if err != nil {
		return err
	}
	return bk.Teardown(result.Window)
}

type sessionProbeEndpoint struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

func newSessionProbeEndpoint() sessionProbeEndpoint {
	return sessionProbeEndpoint{resolve: backend.BackendForTask}
}

func ownedCaptainBackend(home string, meta map[string]string, resolve func(string, map[string]string) (backend.Backend, string, error)) (backend.Backend, error) {
	bk, name, err := resolve(home, meta)
	if err != nil {
		return nil, err
	}
	if expected := meta["backend"]; expected != "" && name != expected {
		return nil, errors.New("captain backend ownership mismatch")
	}
	if name == "herdr" && meta["herdr_session"] != "" {
		sessionID, _ := backend.ParseWindow(meta["window"])
		if sessionID != "" && sessionID != meta["herdr_session"] {
			return nil, errors.New("herdr session ownership mismatch")
		}
	}
	return bk, nil
}

func probeCaptainBackend(bk backend.Backend, window string) (fleet.CaptainProbeResult, error) {
	if aware, ok := bk.(backend.AgentAwareBackend); ok {
		pane, agent, err := aware.CheckAgentAlive(window)
		if errors.Is(err, backend.ErrPaneNotFound) {
			return fleet.CaptainProbeResult{}, nil
		}
		result := fleet.CaptainProbeResult{PaneAlive: pane, AgentAlive: agent, ReadyForPrompt: pane && agent}
		if recognized, ok := bk.(interface {
			IsRecognizedAgent(string) (bool, string)
		}); ok {
			isAgent, status := recognized.IsRecognizedAgent(window)
			result.AgentStatus = status
			result.ReadyForPrompt = isAgent && (status == "idle" || status == "done")
		}
		return result, err
	}
	alive := bk.Alive(window)
	return fleet.CaptainProbeResult{PaneAlive: alive, AgentAlive: alive, ReadyForPrompt: alive}, nil
}

func (e sessionProbeEndpoint) Probe(home string, meta map[string]string) (fleet.CaptainProbeResult, error) {
	bk, err := ownedCaptainBackend(home, meta, e.resolve)
	if err != nil {
		return fleet.CaptainProbeResult{}, err
	}
	return probeCaptainBackend(bk, meta["window"])
}

type sessionNudgeEndpoint struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

func newSessionNudgeEndpoint() sessionNudgeEndpoint {
	return sessionNudgeEndpoint{resolve: backend.BackendForTask}
}

func (e sessionNudgeEndpoint) Nudge(home string, meta map[string]string, payload string) (fleet.NudgeResult, error) {
	bk, err := ownedCaptainBackend(home, meta, e.resolve)
	if err != nil {
		return fleet.NudgeResult{}, err
	}
	result, err := probeCaptainBackend(bk, meta["window"])
	if err != nil {
		return fleet.NudgeResult{}, err
	}
	if !result.PaneAlive || !result.AgentAlive {
		return fleet.NudgeResult{Status: "unavailable"}, nil
	}
	if !result.ReadyForPrompt {
		return fleet.NudgeResult{Status: "deferred", Detail: "agent status: " + result.AgentStatus}, nil
	}
	prompt := backend.SubmitPrompt(bk, meta["window"], payload)
	return fleet.NudgeResult{Status: string(prompt.Status), Detail: prompt.Detail, Acknowledged: prompt.Acknowledged()}, prompt.Err
}

type sessionRetireEndpoint struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
	sleep   func(time.Duration)
}

func newSessionRetireEndpoint() sessionRetireEndpoint {
	return sessionRetireEndpoint{resolve: backend.BackendForTask, sleep: time.Sleep}
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
