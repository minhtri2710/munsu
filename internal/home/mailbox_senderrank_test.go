package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// The receiver derives the sender's rank from durable state rather than
// trusting the rank the envelope carries.
//
// Both halves are pinned here. The acceptance table walks every hop the
// supported topologies actually produce: a derivation that refuses any of them
// is fail-always, not fail-closed. The refusal cases then show that a claimed
// rank which disagrees with the derived one, or one no local state can settle,
// is refused by both Receive and Ack.

const (
	senderRankGeneralID = "general-1"
	senderRankCaptainID = "captain-1"
	senderRankTaskID    = "task:s1"
)

// namedHome makes a home directory whose basename is its General identity.
func namedHome(t *testing.T, identity string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), identity)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return dir
}

// hostSoldier records the durable task the home holds for a soldier it
// dispatched. This is the record the derivation reads.
func hostSoldier(t *testing.T, homeDir, taskID string) {
	t.Helper()
	if err := WriteMeta(homeDir, taskID, map[string]string{"window": "w"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
}

func configureParent(t *testing.T, captainHome, parentHome string) {
	t.Helper()
	if err := config.Set(captainHome, "parent-home", parentHome); err != nil {
		t.Fatalf("config.Set parent-home: %v", err)
	}
}

func hostCaptain(t *testing.T, generalHome, captainID, captainHome string) {
	t.Helper()
	if err := WriteMeta(generalHome, "captain:"+captainID, map[string]string{"kind": "captain", "home": captainHome}); err != nil {
		t.Fatalf("WriteMeta captain: %v", err)
	}
}

// deliver writes env into the receiving home's inbox and returns its ref.
func deliver(t *testing.T, homeDir string, env *Envelope) NotificationRef {
	t.Helper()
	if err := NewStore(homeDir).WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	return NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
}

func TestSenderRankDerivesForEveryHopTheTopologiesProduce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		build    func(t *testing.T) (*Receiver, string)
		envelope func(homeDir string) *Envelope
	}{
		{
			// SendToCaptain: no task ID, sender identity is the General home.
			name: "general to captain",
			build: func(t *testing.T) (*Receiver, string) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatalf("WriteHomeIdentity: %v", err)
				}
				configureParent(t, dir, namedHome(t, senderRankGeneralID))
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatalf("NewReceiver: %v", err)
				}
				return r, dir
			},
			envelope: func(string) *Envelope {
				return &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					Payload: "orders",
				}
			},
		},
		{
			// Config-reread: the same hop carrying the captain's own task ID,
			// which names a record in the General home, not this one.
			name: "general to captain carrying the captain task ID",
			build: func(t *testing.T) (*Receiver, string) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatalf("WriteHomeIdentity: %v", err)
				}
				configureParent(t, dir, namedHome(t, senderRankGeneralID))
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatalf("NewReceiver: %v", err)
				}
				return r, dir
			},
			envelope: func(string) *Envelope {
				return &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					TaskID: "captain:" + senderRankCaptainID, Key: "config-reread",
					Payload: "reread",
				}
			},
		},
		{
			// The captain's uplink report. The General home does hold a task
			// record for the captain, so this is the hop that would misderive
			// if a hosted task alone were taken as soldier provenance.
			name: "captain to general",
			build: func(t *testing.T) (*Receiver, string) {
				dir := namedHome(t, senderRankGeneralID)
				captainHome := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(captainHome, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatalf("WriteHomeIdentity: %v", err)
				}
				hostCaptain(t, dir, senderRankCaptainID, captainHome)
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatalf("NewReceiver: %v", err)
				}
				return r, dir
			},
			envelope: func(string) *Envelope {
				return &Envelope{
					SenderRank: RankCaptain, SenderIdentity: senderRankCaptainID,
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					TaskID: "captain:" + senderRankCaptainID, Key: "phase",
					Payload: "captain report",
				}
			},
		},
		{
			name: "soldier to captain",
			build: func(t *testing.T) (*Receiver, string) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatalf("WriteHomeIdentity: %v", err)
				}
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatalf("NewReceiver: %v", err)
				}
				return r, dir
			},
			envelope: func(string) *Envelope {
				return &Envelope{
					SenderRank: RankSoldier, SenderIdentity: ReceiverIDForTask(senderRankTaskID),
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					TaskID: senderRankTaskID, Key: "phase",
					Payload: "soldier report",
				}
			},
		},
		{
			// Matrix section 1.1: direct General dispatch, so the soldier
			// reports to the General home that hosts it.
			name: "soldier to general",
			build: func(t *testing.T) (*Receiver, string) {
				dir := namedHome(t, senderRankGeneralID)
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatalf("NewReceiver: %v", err)
				}
				return r, dir
			},
			envelope: func(string) *Envelope {
				return &Envelope{
					SenderRank: RankSoldier, SenderIdentity: ReceiverIDForTask(senderRankTaskID),
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					TaskID: senderRankTaskID, Key: "phase",
					Payload: "soldier report",
				}
			},
		},
		{
			name: "captain to soldier",
			build: func(t *testing.T) (*Receiver, string) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatalf("WriteHomeIdentity: %v", err)
				}
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewSoldierReceiver(dir, senderRankTaskID)
				if err != nil {
					t.Fatalf("NewSoldierReceiver: %v", err)
				}
				return r, dir
			},
			envelope: func(string) *Envelope {
				return &Envelope{
					SenderRank: RankCaptain, SenderIdentity: senderRankCaptainID,
					ReceiverRank: RankSoldier, ReceiverID: ReceiverIDForTask(senderRankTaskID),
					TaskID:  senderRankTaskID,
					Payload: "command",
				}
			},
		},
		{
			name: "general to soldier",
			build: func(t *testing.T) (*Receiver, string) {
				dir := namedHome(t, senderRankGeneralID)
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewSoldierReceiver(dir, senderRankTaskID)
				if err != nil {
					t.Fatalf("NewSoldierReceiver: %v", err)
				}
				return r, dir
			},
			envelope: func(string) *Envelope {
				return &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankSoldier, ReceiverID: ReceiverIDForTask(senderRankTaskID),
					TaskID:  senderRankTaskID,
					Payload: "command",
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, dir := tc.build(t)
			env := tc.envelope(dir)
			ref := deliver(t, dir, env)
			if _, err := r.Receive(ref); err != nil {
				t.Fatalf("Receive refused a hop the topology produces: %v", err)
			}
			if _, err := r.Ack(ref); err != nil {
				t.Fatalf("Ack refused a hop the topology produces: %v", err)
			}
			if !NewStore(dir).IsAcked(env.SenderIdentity, env.MessageID) {
				t.Fatal("no ack was written")
			}
		})
	}
}

func TestSenderRankRefusesMissingOrInvalidHomeProvenance(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) (*Receiver, *Envelope)
		want  string
	}{
		{
			name: "captain missing parent home",
			build: func(t *testing.T) (*Receiver, *Envelope) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatal(err)
				}
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatal(err)
				}
				return r, &Envelope{SenderIdentity: senderRankGeneralID, ReceiverID: senderRankCaptainID}
			},
			want: "parent home",
		},
		{
			name: "general missing captain record",
			build: func(t *testing.T) (*Receiver, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatal(err)
				}
				return r, &Envelope{SenderIdentity: senderRankCaptainID, ReceiverID: senderRankGeneralID}
			},
			want: "captain sender provenance",
		},
		{
			name: "general captain record with wrong kind",
			build: func(t *testing.T) (*Receiver, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				if err := WriteMeta(dir, "captain:"+senderRankCaptainID, map[string]string{"kind": "scout", "home": "/missing"}); err != nil {
					t.Fatal(err)
				}
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatal(err)
				}
				return r, &Envelope{SenderIdentity: senderRankCaptainID, ReceiverID: senderRankGeneralID}
			},
			want: "kind",
		},
		{
			name: "general captain record without home",
			build: func(t *testing.T) (*Receiver, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				if err := WriteMeta(dir, "captain:"+senderRankCaptainID, map[string]string{"kind": "captain"}); err != nil {
					t.Fatal(err)
				}
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatal(err)
				}
				return r, &Envelope{SenderIdentity: senderRankCaptainID, ReceiverID: senderRankGeneralID}
			},
			want: "no home",
		},
		{
			name: "general captain home identity mismatch",
			build: func(t *testing.T) (*Receiver, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				captainHome := namedHome(t, "other-captain")
				if err := WriteHomeIdentity(captainHome, "other-captain", RankCaptain); err != nil {
					t.Fatal(err)
				}
				hostCaptain(t, dir, senderRankCaptainID, captainHome)
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatal(err)
				}
				return r, &Envelope{SenderIdentity: senderRankCaptainID, ReceiverID: senderRankGeneralID}
			},
			want: "captain home provenance",
		},
		{
			name: "captain parent home malformed",
			build: func(t *testing.T) (*Receiver, *Envelope) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatal(err)
				}
				parent := t.TempDir()
				if err := os.WriteFile(filepath.Join(parent, captainMarkerName), []byte("broken\n"), 0644); err != nil {
					t.Fatal(err)
				}
				configureParent(t, dir, parent)
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatal(err)
				}
				return r, &Envelope{SenderIdentity: senderRankGeneralID, ReceiverID: senderRankCaptainID}
			},
			want: "parent home provenance",
		},
		{
			name: "captain parent home wrong identity",
			build: func(t *testing.T) (*Receiver, *Envelope) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatal(err)
				}
				configureParent(t, dir, namedHome(t, "other-general"))
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatal(err)
				}
				return r, &Envelope{SenderIdentity: senderRankGeneralID, ReceiverID: senderRankCaptainID}
			},
			want: "parent home provenance",
		},
		{
			name: "general captain home malformed",
			build: func(t *testing.T) (*Receiver, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				captainHome := t.TempDir()
				if err := os.WriteFile(filepath.Join(captainHome, captainMarkerName), []byte("broken\n"), 0644); err != nil {
					t.Fatal(err)
				}
				hostCaptain(t, dir, senderRankCaptainID, captainHome)
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatal(err)
				}
				return r, &Envelope{SenderIdentity: senderRankCaptainID, ReceiverID: senderRankGeneralID}
			},
			want: "captain sender home provenance",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, env := tc.build(t)
			if env.SenderIdentity == senderRankGeneralID {
				env.SenderRank = RankGeneral
			} else {
				env.SenderRank = RankCaptain
			}
			if err := r.verifySenderRank(env); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("verifySenderRank error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSenderRankRefusesCaptainTaskAsSoldier(t *testing.T) {
	dir := namedHome(t, senderRankGeneralID)
	if err := WriteMeta(dir, "captain:"+senderRankCaptainID, map[string]string{"kind": "captain", "home": "/missing"}); err != nil {
		t.Fatal(err)
	}
	env := &Envelope{SenderRank: RankSoldier, SenderIdentity: ReceiverIDForTask("captain:" + senderRankCaptainID), TaskID: "captain:" + senderRankCaptainID}
	r := &Receiver{rank: RankGeneral, store: NewStore(dir)}
	if err := r.verifySenderRank(env); err == nil {
		t.Fatal("verifySenderRank accepted a captain task as a soldier")
	}
}

func TestSenderRankRejectsUnsupportedReceiverRank(t *testing.T) {
	if err := (&Receiver{rank: Rank("unknown"), store: NewStore(t.TempDir())}).verifySenderRank(&Envelope{SenderRank: RankGeneral, SenderIdentity: "sender"}); err == nil || !strings.Contains(err.Error(), "general sender cannot address receiver rank") {
		t.Fatalf("verifySenderRank error = %v, want unsupported-rank refusal", err)
	}
}

func TestSenderRankRefusesUnsupportedClaimCombinations(t *testing.T) {
	dir := namedHome(t, senderRankGeneralID)
	hostSoldier(t, dir, senderRankTaskID)
	cases := []struct {
		name         string
		rank         Rank
		receiverRank Rank
		identity     string
		taskID       string
		want         string
	}{
		{"soldier to soldier", RankSoldier, RankSoldier, ReceiverIDForTask(senderRankTaskID), senderRankTaskID, "soldier sender cannot address receiver rank"},
		{"soldier identity mismatch", RankSoldier, RankGeneral, "other-soldier", senderRankTaskID, "soldier sender identity does not match"},
		{"soldier to unknown receiver", RankSoldier, Rank("unknown"), ReceiverIDForTask(senderRankTaskID), senderRankTaskID, "soldier sender cannot address receiver rank"},
		{"captain to unknown receiver", RankCaptain, Rank("unknown"), senderRankCaptainID, "", "captain sender cannot address receiver rank"},
		{"general to unknown receiver", RankGeneral, Rank("unknown"), senderRankGeneralID, "", "general sender cannot address receiver rank"},
		{"unsupported sender", Rank("unknown"), RankGeneral, "sender", "", "unsupported sender rank"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Receiver{rank: tc.receiverRank, store: NewStore(dir)}
			err := r.verifySenderRank(&Envelope{SenderRank: tc.rank, SenderIdentity: tc.identity, TaskID: tc.taskID})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("verifySenderRank error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSenderRankAcceptsCollidingCaptainAndSoldierClaims(t *testing.T) {
	generalHome := namedHome(t, "general-collision")
	captainHome := namedHome(t, "captain-collision")
	if err := WriteHomeIdentity(captainHome, "task_relay", RankCaptain); err != nil {
		t.Fatal(err)
	}
	hostCaptain(t, generalHome, "task_relay", captainHome)
	hostSoldier(t, generalHome, "task:relay")
	r, err := NewReceiver(generalHome)
	if err != nil {
		t.Fatal(err)
	}

	for _, env := range []*Envelope{
		{SenderRank: RankCaptain, SenderIdentity: "task_relay", ReceiverRank: RankGeneral, ReceiverID: "general-collision", TaskID: "task:relay", Key: "report", Payload: "captain report"},
		{SenderRank: RankSoldier, SenderIdentity: ReceiverIDForTask("task:relay"), ReceiverRank: RankGeneral, ReceiverID: "general-collision", TaskID: "task:relay", Key: "report", Payload: "soldier report"},
	} {
		ref := deliver(t, generalHome, env)
		if _, err := r.Receive(ref); err != nil {
			t.Fatalf("Receive refused colliding %q claim: %v", env.SenderRank, err)
		}
		if _, err := r.Ack(ref); err != nil {
			t.Fatalf("Ack refused colliding %q claim: %v", env.SenderRank, err)
		}
	}
}

func TestSenderRankRefusesWhatProvenanceContradicts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		build   func(t *testing.T) (*Receiver, string, *Envelope)
		wantSub string
	}{
		{
			// The one-hop table says a captain may address a general. Claiming
			// captain is how a soldier of that home would reach for the edge it
			// is not on, and the task record it sends under settles the rank.
			name: "a hosted soldier claiming captain rank",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatalf("NewReceiver: %v", err)
				}
				return r, dir, &Envelope{
					SenderRank: RankCaptain, SenderIdentity: ReceiverIDForTask(senderRankTaskID),
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					TaskID: senderRankTaskID, Key: "phase",
					Payload: "claims captain",
				}
			},
			wantSub: "sender rank underivable",
		},
		{
			// The home owner is the only sender a soldier receiver can place.
			name: "a soldier receiver reached by a stranger",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewSoldierReceiver(dir, senderRankTaskID)
				if err != nil {
					t.Fatalf("NewSoldierReceiver: %v", err)
				}
				return r, dir, &Envelope{
					SenderRank: RankCaptain, SenderIdentity: "some-other-captain",
					ReceiverRank: RankSoldier, ReceiverID: ReceiverIDForTask(senderRankTaskID),
					TaskID:  senderRankTaskID,
					Payload: "command",
				}
			},
			wantSub: "sender rank underivable",
		},
		{
			name: "a captain whose parent home does not exist",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatalf("WriteHomeIdentity: %v", err)
				}
				configureParent(t, dir, filepath.Join(t.TempDir(), senderRankGeneralID))
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatalf("NewReceiver: %v", err)
				}
				return r, dir, &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					Payload: "orders",
				}
			},
			wantSub: "sender rank underivable",
		},
		{
			// Without readable provenance for the receiving home there is no
			// rank to derive against, so nothing is accepted on trust.
			name: "a receiving home whose provenance is unreadable",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewSoldierReceiver(dir, senderRankTaskID)
				if err != nil {
					t.Fatalf("NewSoldierReceiver: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, captainMarkerName), []byte("garbage\n"), 0644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				return r, dir, &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankSoldier, ReceiverID: ReceiverIDForTask(senderRankTaskID),
					TaskID:  senderRankTaskID,
					Payload: "command",
				}
			},
			wantSub: "sender rank underivable",
		},
		{
			// A task ID no home can hold names no task record, so it carries
			// no soldier provenance however the sender identifies itself.
			name: "a soldier claim under a task ID that names no record",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				dir := namedHome(t, senderRankCaptainID)
				if err := WriteHomeIdentity(dir, senderRankCaptainID, RankCaptain); err != nil {
					t.Fatalf("WriteHomeIdentity: %v", err)
				}
				r, err := NewReceiver(dir)
				if err != nil {
					t.Fatalf("NewReceiver: %v", err)
				}
				return r, dir, &Envelope{
					SenderRank: RankSoldier, SenderIdentity: ".",
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					TaskID: ".", Key: "phase",
					Payload: "claims soldier",
				}
			},
			wantSub: "sender rank underivable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, dir, env := tc.build(t)
			ref := deliver(t, dir, env)
			if _, err := r.Receive(ref); err == nil {
				t.Fatal("Receive accepted a sender rank provenance contradicts")
			} else if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("receive error = %v, want %q", err, tc.wantSub)
			}
			if _, err := r.Ack(ref); err == nil {
				t.Fatal("Ack accepted a sender rank provenance contradicts")
			} else if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("ack error = %v, want %q", err, tc.wantSub)
			}
			if NewStore(dir).IsAcked(env.SenderIdentity, env.MessageID) {
				t.Fatal("a refused envelope received an ack")
			}
		})
	}
}
