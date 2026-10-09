package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

const pushGrantCommandHead = "6503e5e9852b028c95c8d1c9cd9178667506ac7b"

func TestDeliveryPushGrantRecordsCurrentTaskHeadAndWords(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "grant-command")
	if err := home.WriteMeta(homeDir, "grant-command", map[string]string{"id": "grant-command", "kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	before, err := auth.Get(mustTaskIDFor(t, "grant-command"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runTaskCommand(t, []string{
		"delivery", "push-grant", "grant-command",
		"--head", pushGrantCommandHead,
		"--grantor", "Human",
		"--channel", "supervisor-relay:typed",
		"--quote", "approve push",
		"--home", homeDir,
	})
	if err != nil {
		t.Fatalf("delivery push-grant: %v\n%s", err, out)
	}
	found, err := auth.HasPushGrant(mustTaskIDFor(t, "grant-command"), pushGrantCommandHead)
	if err != nil || !found {
		t.Fatalf("canonical grant lookup = %v, %v; want exact recorded head", found, err)
	}
	after, err := auth.Get(mustTaskIDFor(t, "grant-command"))
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation || after.Revision != before.Revision {
		t.Fatalf("grant changed aggregate generation/revision: before=(%d,%d), after=(%d,%d)", before.Generation, before.Revision, after.Generation, after.Revision)
	}
	if !strings.Contains(out, pushGrantCommandHead) || !strings.Contains(out, "grant-command") {
		t.Fatalf("push-grant confirmation omits task or head: %s", out)
	}
}

func TestDeliveryPushGrantRejectsMalformedInputWithoutRecording(t *testing.T) {
	for _, tc := range []struct {
		name  string
		head  string
		quote string
	}{
		{name: "short head", head: "abcd", quote: "approve push"},
		{name: "empty quote", head: pushGrantCommandHead, quote: " \t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			auth := testAuthorityFor(t, homeDir)
			seedAuthorityTask(t, auth, "grant-invalid")
			if err := home.WriteMeta(homeDir, "grant-invalid", map[string]string{"id": "grant-invalid", "kind": "ship"}); err != nil {
				t.Fatal(err)
			}
			out, err := runTaskCommand(t, []string{
				"delivery", "push-grant", "grant-invalid",
				"--head", tc.head,
				"--grantor", "Human",
				"--channel", "supervisor-relay:typed",
				"--quote", tc.quote,
				"--home", homeDir,
			})
			if err == nil {
				t.Fatalf("delivery push-grant accepted invalid input: %s", out)
			}
			found, lookupErr := auth.HasPushGrant(mustTaskIDFor(t, "grant-invalid"), pushGrantCommandHead)
			if lookupErr != nil || found {
				t.Fatalf("invalid grant left canonical record: found=%v err=%v", found, lookupErr)
			}
		})
	}
}
