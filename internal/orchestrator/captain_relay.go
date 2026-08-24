package orchestrator

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/config"
)

type watcherHooks struct {
	notification NotificationTransport
	activation   ActivationTransport
}

func NewCaptainWatcherHooks(notification NotificationTransport, activation ActivationTransport) WatcherHooks {
	return watcherHooks{notification: notification, activation: activation}
}
func (h watcherHooks) Reconcile(homeDir string, startup bool) error {
	return ReconcileCaptainHook(homeDir, startup, h.notification)
}
func (h watcherHooks) Activate(homeDir string) { CaptainActivationHook(homeDir, h.activation) }

// resolveParentHome resolves the parent home directory for a captain context.
// Precedence:
//  1. MUNSU_PARENT_STATUS env var (if set and not equal to homeDir)
//  2. config/parent-home (if set and not equal to homeDir)
//  3. empty string (no parent)
//
// The function is safe to call from watcher hooks that may not inherit the
// original process environment (crash restart, plain `munsu watch --home`).
// It never returns homeDir as a valid parent (self-referencing guard).
func ResolveCaptainParentHome(homeDir string) string {
	// 1. Check env var
	if p := os.Getenv("MUNSU_PARENT_STATUS"); p != "" && p != homeDir {
		return p
	}

	// 2. Fall back to config/parent-home (durable, survives env loss)
	if p, err := config.Get(homeDir, "parent-home"); err == nil && p != "" && p != homeDir {
		return p
	}

	// 3. No parent
	return ""
}

// captainActivationHook is the per-cycle activation hook running inside a
// captain home. It nudges the captain agent pane when new soldier receipts
// arrive, without waiting for General round-trip.
func CaptainActivationHook(homeDir string, activation ActivationTransport) {
	parentHome := ResolveCaptainParentHome(homeDir)
	if parentHome == "" {
		return
	}
	ActivateOnReceiptWithTransport(homeDir, parentHome, activation)
}

// ReconcileCaptainHook recovers mailbox uplinks for a home on watcher
// startup and each polling cycle.
func ReconcileCaptainHook(homeDir string, startup bool, transport NotificationTransport) error {
	if transport == nil {
		return fmt.Errorf("uplink notification transport capability is required")
	}
	parentHome := ResolveCaptainParentHome(homeDir)
	if parentHome == "" {
		_, err := Recover(RecoverRequest{
			SenderHome: homeDir, ReceiverHome: homeDir,
			ReceiverRank: RankGeneral, ForceNotify: startup,
			Notify: func(ref NotificationRef) UplinkNotifyResult {
				return NotifyParentWithTransport(homeDir, homeDir, ref, transport)
			},
		})
		return err
	}
	if _, err := Recover(RecoverRequest{
		SenderHome: homeDir, ReceiverHome: homeDir,
		ReceiverRank: RankCaptain, ForceNotify: startup,
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			return NotifyParentWithTransport(homeDir, homeDir, ref, transport)
		},
	}); err != nil {
		return err
	}
	_, err := Recover(RecoverRequest{
		SenderHome: homeDir, ReceiverHome: parentHome,
		ReceiverRank: RankGeneral, ForceNotify: startup,
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			return NotifyParentWithTransport(homeDir, parentHome, ref, transport)
		},
	})
	return err
}
