package orchestrator

import (
	"testing"
)

type notifyTransport struct{ submitted string }

func (t *notifyTransport) Notify(_ string, _ TargetResult, payload string) UplinkNotifyResult {
	t.submitted = payload
	return UplinkNotifyResult{Acknowledged: true}
}

func TestNotifyParentWithTargetResolverSubmitsOnlyNotificationRef(t *testing.T) {
	ref := NotificationRef{MessageID: "message-one", SenderIdentity: "soldier-one"}
	transport := &notifyTransport{}
	var receiverSeen string
	result := NotifyParentWithTargetResolver("sender", "receiver", ref,
		func(receiver string, _ bool, got NotificationRef) (TargetResult, error) {
			receiverSeen = receiver
			return TargetResult{Source: RuntimeSource, Handle: "fleet:p9"}, nil
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
		func(string, bool, NotificationRef) (TargetResult, error) {
			return TargetResult{Source: Unsupported}, nil
		}, &notifyTransport{})
	if !result.Queued || result.Acknowledged {
		t.Fatalf("result = %+v", result)
	}
}
