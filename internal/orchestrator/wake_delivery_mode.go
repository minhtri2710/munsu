package orchestrator

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
)

type WakeDeliveryMode string

const (
	WakeDeliveryNative WakeDeliveryMode = "native"
	WakeDeliveryHerdr  WakeDeliveryMode = "herdr"
	WakeDeliveryManual WakeDeliveryMode = "manual"
)

func ResolveWakeDeliveryMode(homeDir string) (WakeDeliveryMode, error) {
	value, err := config.Get(homeDir, "wake-delivery-mode")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return WakeDeliveryNative, nil
		}
		return "", err
	}
	mode := WakeDeliveryMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case WakeDeliveryNative, WakeDeliveryHerdr, WakeDeliveryManual:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported wake-delivery-mode %q", value)
	}
}

const wakeDispatchRefusedEvent = "wake.dispatch.refused"

// wakeModeUnusableReason names the key and the fix for a mode that cannot be
// read or is not one of the supported values.
func wakeModeUnusableReason(err error) string {
	return fmt.Sprintf("config/wake-delivery-mode is unusable: %v; fix with 'munsu config set wake-delivery-mode native|herdr|manual'", err)
}

// RecordWakeDispatchRefusal appends one wake.dispatch.refused event to the
// General home's event log for an unusable wake-delivery-mode, and nothing
// while the reason is unchanged.
func RecordWakeDispatchRefusal(homeDir string, err error) {
	recordRefusalOnce(homeDir, wakeDispatchRefusedEvent, "munsu:wake-dispatch", homeDir, wakeModeUnusableReason(err))
}
