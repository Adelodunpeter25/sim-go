package android

import (
	"context"
	"fmt"
	"strings"
)

// SetAppearance switches the emulator between dark and light mode.
func (Driver) SetAppearance(ctx context.Context, id, mode string) error {
	var night string
	switch strings.ToLower(mode) {
	case "dark":
		night = "yes"
	case "light":
		night = "no"
	default:
		return fmt.Errorf("unknown appearance %q (want dark|light)", mode)
	}
	_, err := adb(ctx, "-s", id, "shell", "cmd", "uimode", "night", night)
	return err
}
