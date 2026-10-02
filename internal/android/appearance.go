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

// Appearance reports the emulator's night mode: "dark" or "light". When the
// device is in an automatic or scheduled mode the raw setting ("auto",
// "custom_schedule", "custom_bedtime") is returned, since the effective mode
// is not exposed by the shell command.
func (Driver) Appearance(ctx context.Context, id string) (string, error) {
	out, err := adb(ctx, "-s", id, "shell", "cmd", "uimode", "night")
	if err != nil {
		return "", err
	}
	return parseAndroidAppearance(string(out))
}

func parseAndroidAppearance(out string) (string, error) {
	const prefix = "night mode:"
	s := strings.ToLower(strings.TrimSpace(out))
	if !strings.HasPrefix(s, prefix) {
		return "", fmt.Errorf("unexpected uimode output %q", strings.TrimSpace(out))
	}
	switch v := strings.TrimSpace(strings.TrimPrefix(s, prefix)); v {
	case "yes":
		return "dark", nil
	case "no":
		return "light", nil
	case "auto", "custom_schedule", "custom_bedtime":
		return v, nil
	default:
		return "", fmt.Errorf("unexpected uimode output %q", strings.TrimSpace(out))
	}
}
