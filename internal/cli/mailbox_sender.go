package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// sessionMailboxSender delivers mailbox notifications to a captain's pane, as
// its .meta binds it.
type sessionMailboxSender struct {
	resolve boundResolver
}

func newSessionMailboxSender() sessionMailboxSender {
	return sessionMailboxSender{resolve: backend.BackendForTask}
}

func (s sessionMailboxSender) Alive(home string, meta map[string]string) (bool, error) {
	return mailboxAlive(home, backend.MetaEndpointRef(meta), meta["harness"], s.resolve)
}

func (s sessionMailboxSender) Send(home string, meta map[string]string, payload string) mhome.BoundSendResult {
	return mailboxSend(home, backend.MetaEndpointRef(meta), payload, s.resolve)
}

// sessionRecoverySender delivers a recovered mailbox envelope to the soldier
// pane its task's canonical endpoint binding names.
type sessionRecoverySender struct {
	resolve boundResolver
}

func newSessionRecoverySender() sessionRecoverySender {
	return sessionRecoverySender{resolve: backend.BackendForTask}
}

func (s sessionRecoverySender) Alive(home string, ep taskauthority.EndpointBinding, harness string) (bool, error) {
	return mailboxAlive(home, bindingRef(ep), harness, s.resolve)
}

func (s sessionRecoverySender) Send(home string, ep taskauthority.EndpointBinding, payload string) mhome.BoundSendResult {
	return mailboxSend(home, bindingRef(ep), payload, s.resolve)
}

func mailboxBackend(home string, ep backend.EndpointRef, resolve boundResolver) (backend.Backend, error) {
	if home == "" || ep.Handle == "" {
		return nil, fmt.Errorf("bound sender identity is incomplete")
	}
	bk, _, err := boundBackend(home, ep, resolve)
	return bk, err
}

func mailboxAlive(home string, ep backend.EndpointRef, harness string, resolve boundResolver) (bool, error) {
	bk, err := mailboxBackend(home, ep, resolve)
	if err != nil {
		return false, err
	}
	result, err := probeCaptainBackend(bk, ep.Handle, harness)
	if err != nil {
		return false, err
	}
	return result.PaneAlive && result.AgentAlive, nil
}

func mailboxSend(home string, ep backend.EndpointRef, payload string, resolve boundResolver) mhome.BoundSendResult {
	bk, err := mailboxBackend(home, ep, resolve)
	if err != nil {
		return mhome.BoundSendResult{Status: "backend-failed", Err: err}
	}
	if recognized, ok := bk.(backend.AgentActivityReader); ok {
		isAgent, status := recognized.IsRecognizedAgent(ep.Handle)
		if !isAgent || !backend.AgentStatusReady(status) {
			return mhome.BoundSendResult{Status: "deferred", Detail: "agent status: " + status}
		}
	}
	result := backend.SubmitPrompt(bk, ep.Handle, payload)
	return mhome.BoundSendResult{Status: string(result.Status), Detail: result.Detail, Acknowledged: result.Acknowledged(), Err: result.Err}
}
