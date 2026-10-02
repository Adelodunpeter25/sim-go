package ios

import (
	"context"
	"fmt"
	"strings"
)

// SetAppearance switches the simulator between dark and light mode.
func (Driver) SetAppearance(ctx context.Context, udid, mode string) error {
	mode = strings.ToLower(mode)
	if mode != "dark" && mode != "light" {
		return fmt.Errorf("unknown appearance %q (want dark|light)", mode)
	}
	_, err := xcrun(ctx, "simctl", "ui", udid, "appearance", mode)
	return err
}

// Appearance reports the simulator's current mode: "dark" or "light".
func (Driver) Appearance(ctx context.Context, udid string) (string, error) {
	out, err := xcrun(ctx, "simctl", "ui", udid, "appearance")
	if err != nil {
		return "", err
	}
	return parseIOSAppearance(string(out))
}

func parseIOSAppearance(out string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(out))
	if s == "dark" || s == "light" {
		return s, nil
	}
	if s == "unknown" {
		return "", fmt.Errorf("appearance unavailable: simulator is not booted")
	}
	return "", fmt.Errorf("unexpected appearance output %q", strings.TrimSpace(out))
}
