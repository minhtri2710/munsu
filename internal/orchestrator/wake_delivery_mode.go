package orchestrator

import (
	"fmt"
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
		return WakeDeliveryNative, nil
	}
	mode := WakeDeliveryMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case WakeDeliveryNative, WakeDeliveryHerdr, WakeDeliveryManual:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported wake-delivery-mode %q", value)
	}
}
