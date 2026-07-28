package orchestrator

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/afk"
)

type notifyTransport struct{ submitted string }

func (t *notifyTransport) Notify(_ string, _ afk.TargetResult, payload string) UplinkNotifyResult {
	t.submitted = payload
	return UplinkNotifyResult{Acknowledged: true}
}

func TestNotifyParentWithTargetResolverSubmitsOnlyNotificationRef(t *testing.T) {
	ref := NotificationRef{MessageID: "message-one", SenderIdentity: "soldier-one"}
	transport := &notifyTransport{}
	var receiverSeen string
	result := NotifyParentWithTargetResolver("sender", "receiver", ref,
		func(receiver string, got NotificationRef) (afk.TargetResult, error) {
			receiverSeen = receiver
			return afk.TargetResult{Source: afk.RuntimeSource, Handle: "fleet:p9"}, nil
		}, transport)
	if !result.Acknowledged {
		t.Fatal("notification should be acknowledged")
	}
	if receiverSeen != "receiver" {
		t.Fatalf("receiver=%q", receiverSeen)
	}
	if transport.submitted != ref.Encode() {
		t.Fatalf("submitted=%q want=%q", transport.submitted, ref.Encode())
	}
	if transport.submitted == "done: raw payload [task=task:with/slash]" {
		t.Fatal("raw payload submitted")
	}
}

func TestNotifyParentWithTargetResolverQueuesUnavailableTarget(t *testing.T) {
	result := NotifyParentWithTargetResolver("sender", "receiver", NotificationRef{},
		func(string, NotificationRef) (afk.TargetResult, error) {
			return afk.TargetResult{Source: afk.Unsupported}, nil
		}, &notifyTransport{})
	if !result.Queued || result.Acknowledged {
		t.Fatalf("result = %+v", result)
	}
}
