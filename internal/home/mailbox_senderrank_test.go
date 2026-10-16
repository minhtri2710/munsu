package home

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// hostSoldier records that the home hosts a live seat for a soldier it
// dispatched. seatsOf answers from these records, standing in for the Task
// Authority check the CLI injects.
func hostSoldier(t *testing.T, homeDir, taskID string) {
	t.Helper()
	dir := filepath.Join(homeDir, "test-seats")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll seats: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, url.PathEscape(taskID)), nil, 0600); err != nil {
		t.Fatalf("WriteFile seat: %v", err)
	}
}

func seatsOf(homeDir string) SoldierSeatCheck {
	return func(taskID string) error {
		info, err := os.Stat(filepath.Join(homeDir, "test-seats", url.PathEscape(taskID)))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("home %s hosts no live soldier seat for task %q", homeDir, taskID)
		}
		return nil
	}
}

func configureParent(t *testing.T, captainHome, parentHome string) {
	t.Helper()
	configDir := filepath.Join(captainHome, "config")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("MkdirAll config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "parent-home"), []byte(parentHome+"\n"), 0600); err != nil {
		t.Fatalf("WriteFile parent-home: %v", err)
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
				if err := SeedCaptainProvenance(dir, senderRankCaptainID); err != nil {
					t.Fatalf("SeedCaptainProvenance: %v", err)
				}
				configureParent(t, dir, namedHome(t, senderRankGeneralID))
				r, err := NewReceiver(dir, seatsOf(dir))
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
				if err := SeedCaptainProvenance(dir, senderRankCaptainID); err != nil {
					t.Fatalf("SeedCaptainProvenance: %v", err)
				}
				configureParent(t, dir, namedHome(t, senderRankGeneralID))
				r, err := NewReceiver(dir, seatsOf(dir))
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
			// A captain's message to the General. The General home does hold a task
			// record for the captain, so this is the hop that would misderive
			// if a hosted task alone were taken as soldier provenance.
			name: "captain to general",
			build: func(t *testing.T) (*Receiver, string) {
				dir := namedHome(t, senderRankGeneralID)
				captainHome := namedHome(t, senderRankCaptainID)
				if err := SeedCaptainProvenance(captainHome, senderRankCaptainID); err != nil {
					t.Fatalf("SeedCaptainProvenance: %v", err)
				}
				hostCaptain(t, dir, senderRankCaptainID, captainHome)
				r, err := NewReceiver(dir, seatsOf(dir))
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
				if err := SeedCaptainProvenance(dir, senderRankCaptainID); err != nil {
					t.Fatalf("SeedCaptainProvenance: %v", err)
				}
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewReceiver(dir, seatsOf(dir))
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
				r, err := NewReceiver(dir, seatsOf(dir))
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
				if err := SeedCaptainProvenance(dir, senderRankCaptainID); err != nil {
					t.Fatalf("SeedCaptainProvenance: %v", err)
				}
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewSoldierReceiver(dir, senderRankTaskID, seatsOf(dir))
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
				r, err := NewSoldierReceiver(dir, senderRankTaskID, seatsOf(dir))
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
			if !NewStore(dir).IsAcked(env) {
				t.Fatal("no ack was written")
			}
		})
	}
}

func TestSenderRankAcceptsCollidingCaptainAndSoldierClaims(t *testing.T) {
	generalHome := namedHome(t, "general-collision")
	captainHome := namedHome(t, "captain-collision")
	if err := SeedCaptainProvenance(captainHome, "task_relay"); err != nil {
		t.Fatal(err)
	}
	hostCaptain(t, generalHome, "task_relay", captainHome)
	hostSoldier(t, generalHome, "task:relay")
	r, err := NewReceiver(generalHome, seatsOf(generalHome))
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

// newCaptainReceiver makes a seeded captain home and its receiver.
func newCaptainReceiver(t *testing.T) (*Receiver, string) {
	t.Helper()
	dir := namedHome(t, senderRankCaptainID)
	if err := SeedCaptainProvenance(dir, senderRankCaptainID); err != nil {
		t.Fatalf("SeedCaptainProvenance: %v", err)
	}
	r, err := NewReceiver(dir, seatsOf(dir))
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return r, dir
}

// newGeneralReceiver makes a markerless General home and its receiver.
func newGeneralReceiver(t *testing.T) (*Receiver, string) {
	t.Helper()
	dir := namedHome(t, senderRankGeneralID)
	r, err := NewReceiver(dir, seatsOf(dir))
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return r, dir
}

// Every row delivers an envelope the one-hop table admits and whose claimed
// rank the receiving home's durable state cannot prove; Receive and Ack both
// refuse it with the message of the one provenance check that failed. The
// allow side is TestSenderRankDerivesForEveryHopTheTopologiesProduce.
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
				r, dir := newGeneralReceiver(t)
				hostSoldier(t, dir, senderRankTaskID)
				return r, dir, &Envelope{
					SenderRank: RankCaptain, SenderIdentity: ReceiverIDForTask(senderRankTaskID),
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					TaskID: senderRankTaskID, Key: "phase",
					Payload: "claims captain",
				}
			},
			wantSub: "reading captain sender provenance",
		},
		{
			name: "a captain task record of another kind",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newGeneralReceiver(t)
				if err := WriteMeta(dir, "captain:"+senderRankCaptainID, map[string]string{"kind": "scout", "home": "/missing"}); err != nil {
					t.Fatal(err)
				}
				return r, dir, &Envelope{
					SenderRank: RankCaptain, SenderIdentity: senderRankCaptainID,
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					Payload: "captain report",
				}
			},
			wantSub: "captain sender provenance has kind",
		},
		{
			name: "a captain task record with no home",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newGeneralReceiver(t)
				if err := WriteMeta(dir, "captain:"+senderRankCaptainID, map[string]string{"kind": "captain"}); err != nil {
					t.Fatal(err)
				}
				return r, dir, &Envelope{
					SenderRank: RankCaptain, SenderIdentity: senderRankCaptainID,
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					Payload: "captain report",
				}
			},
			wantSub: "captain sender provenance has no home",
		},
		{
			name: "a captain task record pointing at another captain's home",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newGeneralReceiver(t)
				captainHome := namedHome(t, "other-captain")
				if err := SeedCaptainProvenance(captainHome, "other-captain"); err != nil {
					t.Fatal(err)
				}
				hostCaptain(t, dir, senderRankCaptainID, captainHome)
				return r, dir, &Envelope{
					SenderRank: RankCaptain, SenderIdentity: senderRankCaptainID,
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					Payload: "captain report",
				}
			},
			wantSub: "captain home provenance is",
		},
		{
			// The strict marker check: two lines named a captain under the
			// lax reader and no longer do.
			name: "a captain home whose marker has two lines",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newGeneralReceiver(t)
				captainHome := t.TempDir()
				if err := os.WriteFile(filepath.Join(captainHome, CaptainProvenanceMarkerName), []byte("munsu-v2\n"+senderRankCaptainID+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
				hostCaptain(t, dir, senderRankCaptainID, captainHome)
				return r, dir, &Envelope{
					SenderRank: RankCaptain, SenderIdentity: senderRankCaptainID,
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					Payload: "captain report",
				}
			},
			wantSub: "reading captain sender home provenance",
		},
		{
			// The home owner is the only sender a soldier receiver can place.
			name: "a soldier receiver reached by a stranger",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewSoldierReceiver(dir, senderRankTaskID, seatsOf(dir))
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
			wantSub: "hosting home provenance is",
		},
		{
			name: "a soldier receiver reached by a general that does not host it",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				dir := namedHome(t, "hosting-home")
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewSoldierReceiver(dir, senderRankTaskID, seatsOf(dir))
				if err != nil {
					t.Fatalf("NewSoldierReceiver: %v", err)
				}
				return r, dir, &Envelope{
					SenderRank: RankGeneral, SenderIdentity: "other-general",
					ReceiverRank: RankSoldier, ReceiverID: ReceiverIDForTask(senderRankTaskID),
					TaskID:  senderRankTaskID,
					Payload: "command",
				}
			},
			wantSub: "hosting home provenance is",
		},
		{
			name: "a captain with no parent home configured",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newCaptainReceiver(t)
				return r, dir, &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					Payload: "orders",
				}
			},
			wantSub: "reading captain parent home",
		},
		{
			name: "a captain whose parent home does not exist",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newCaptainReceiver(t)
				configureParent(t, dir, filepath.Join(t.TempDir(), senderRankGeneralID))
				return r, dir, &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					Payload: "orders",
				}
			},
			wantSub: "reading parent home provenance",
		},
		{
			name: "a captain whose parent home is another general",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newCaptainReceiver(t)
				configureParent(t, dir, namedHome(t, "other-general"))
				return r, dir, &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					Payload: "orders",
				}
			},
			wantSub: "parent home provenance is",
		},
		{
			// Without readable provenance for the receiving home there is no
			// rank to derive against, so nothing is accepted on trust.
			name: "a receiving home whose provenance is unreadable",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				dir := namedHome(t, senderRankGeneralID)
				hostSoldier(t, dir, senderRankTaskID)
				r, err := NewSoldierReceiver(dir, senderRankTaskID, seatsOf(dir))
				if err != nil {
					t.Fatalf("NewSoldierReceiver: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, CaptainProvenanceMarkerName), []byte("garbage\n"), 0644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				return r, dir, &Envelope{
					SenderRank: RankGeneral, SenderIdentity: senderRankGeneralID,
					ReceiverRank: RankSoldier, ReceiverID: ReceiverIDForTask(senderRankTaskID),
					TaskID:  senderRankTaskID,
					Payload: "command",
				}
			},
			wantSub: "reading soldier hosting home provenance",
		},
		{
			// A task ID no home can hold names no task record, so it carries
			// no soldier provenance however the sender identifies itself.
			name: "a soldier claim under a task ID that names no record",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newCaptainReceiver(t)
				return r, dir, &Envelope{
					SenderRank: RankSoldier, SenderIdentity: ".",
					ReceiverRank: RankCaptain, ReceiverID: senderRankCaptainID,
					TaskID: ".", Key: "phase",
					Payload: "claims soldier",
				}
			},
			wantSub: "reading soldier sender provenance",
		},
		{
			name: "a soldier claim under an identity that is not its task's",
			build: func(t *testing.T) (*Receiver, string, *Envelope) {
				r, dir := newGeneralReceiver(t)
				hostSoldier(t, dir, senderRankTaskID)
				return r, dir, &Envelope{
					SenderRank: RankSoldier, SenderIdentity: "other-soldier",
					ReceiverRank: RankGeneral, ReceiverID: senderRankGeneralID,
					TaskID: senderRankTaskID, Key: "phase",
					Payload: "soldier report",
				}
			},
			wantSub: "soldier sender identity does not match",
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
			if NewStore(dir).IsAcked(env) {
				t.Fatal("a refused envelope received an ack")
			}
		})
	}
}
