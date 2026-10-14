package fleet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/fence"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
)

// CaptainSeatEnv names the seat socket in a fenced captain's environment.
const CaptainSeatEnv = "MUNSU_CAPTAIN_SEAT"

const captainSeatSocketName = ".captain-seat.sock"

func captainSeatSocket(captainHome string) string {
	return filepath.Join(home.StateDir(captainHome), captainSeatSocketName)
}

// captainSeatVerbs are the munsu commands of the captain charter, and the
// hooks its harness integration runs, that write the captain's records or
// drive its soldiers' panes. Inside the fence they run through the seat,
// outside it; every other command runs in the fence, where a write to a
// record, configuration or program fails. git-guard stays in the fence: an
// unfenced git with captain-chosen options runs anything. Human words
// (domain.Words): grantor the Human, channel direct chat with the Lead,
// 2026-10-13, words=selected, 2A: "Captain được gửi input vào session của
// chính soldier của nó qua munsu" (the seat relays send, spawn and teardown
// into its own soldiers' sessions, under G1085).
var captainSeatVerbs = map[string]bool{
	"brief": true, "delivery": true, "guard": true, "inbox": true, "peek": true,
	"promote": true, "report": true, "send": true, "session-start": true,
	"soldier-state": true, "spawn": true, "task": true, "teardown": true,
	"turnend": true, "wake": true,
}

// CaptainSeatRuns reports whether a fenced captain's munsu args name a
// command its seat runs.
func CaptainSeatRuns(args []string) bool {
	return len(args) > 0 && captainSeatVerbs[args[0]]
}

// captainSeat is a captain launch's write fence, probed, with the PATH it
// denies and the pi agent dir the captain runs from. A nil fence is a launch
// on a host with no fence, recorded with its reason.
type captainSeat struct {
	fence    *fence.Fence
	digest   string
	pathDirs []string
	agentDir string
	reason   string
}

// prepareCaptainSeat builds the captain's fence, F3, and proves it before any
// pane exists, as probeFence does for a soldier. The captain gets its own pi
// agent dir, built like a soldier's, and its docs dir. A fence that cannot be
// built or proved refuses the launch. A host with no fence launches the captain
// unfenced and records why: a captain has no project to carry
// fence-unsupported, and off darwin it launched unfenced before this fence
// existed. Human words (domain.Words): grantor the Human, channel
// supervisor-relay:dialog, words=selected, G1085: "Quy tắc chung
// (Recommended): Seat có fence không bao giờ ghi chương trình hay cấu hình mà
// tiến trình ngoài fence sẽ chạy, và không gửi input vào session ngoài session
// của nó."; the design: grantor the Human, channel direct chat with the Lead,
// 2026-10-13, words=selected, 1A: "Xác nhận cả 7 refusal đã ghi theo tự quyết
// đi" (ratifies the "tự quyết đi" record, scope: the captain-fence design).
func prepareCaptainSeat(captainHome, parentHome, captainID, harnessName string) (captainSeat, error) {
	if err := fence.Supported(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: captain %s launches UNFENCED: %v\n", captainID, err)
		return captainSeat{reason: err.Error()}, nil
	}
	stateDir := home.StateDir(captainHome)
	agentDir, err := piAgentDirPath(captainHome, taskIDForCaptain(captainID))
	if err != nil {
		return captainSeat{}, err
	}
	src, err := harness.HumanPiAgentDir(stateDir)
	if err != nil {
		return captainSeat{}, fmt.Errorf("captain fence: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, fence.CaptainDocsDir), 0o755); err != nil {
		return captainSeat{}, fmt.Errorf("captain fence: %w", err)
	}
	if err := harness.BuildPiAgentDir(src, harness.SeatBuildDir(captainHome), agentDir); err != nil {
		return captainSeat{}, fmt.Errorf("captain fence: %w", err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return captainSeat{}, fmt.Errorf("captain fence: resolving user home: %w", err)
	}
	launch := fence.Launch{
		Role: fence.RoleCaptain, Harness: harnessName, Home: captainHome, ParentHome: parentHome,
		UserHome: userHome,
		Socket:   captainSeatSocket(captainHome), HarnessStateDir: agentDir,
		PathDirs: seatPathDirs(captainHome), ShimDir: gitShimDir(captainHome),
	}
	if _, gitDir, commonDir, err := ClassifyIdentity(captainHome); err == nil {
		launch.GitDir, launch.CommonDir = gitDir, commonDir
	}
	f, err := fence.New(launch)
	if err != nil {
		return captainSeat{}, fmt.Errorf("captain fence: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fenceProbeTimeout)
	defer cancel()
	ev, err := f.Probe(ctx)
	if err != nil {
		return captainSeat{}, fmt.Errorf("captain fence refused the launch: %w", err)
	}
	return captainSeat{fence: f, digest: ev.ProfileDigest, pathDirs: launch.PathDirs, agentDir: agentDir}, nil
}

// meta is the seat's fence record in the captain's task meta.
func (s captainSeat) meta() map[string]string {
	if s.fence == nil {
		return map[string]string{"fence": "unfenced", "fence_reason": s.reason}
	}
	return map[string]string{"fence": "applied", "fence_profile_digest": s.digest}
}
