package ios

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func (Driver) Tap(ctx context.Context, udid string, x, y int) error {
	_, _, _ = ctx, x, y
	// This Xcode's `simctl io` only offers enumerate/poll/recordVideo/
	// screenshot (verified via `simctl io --help`): no tap/swipe/button.
	// t3code solves this with an agent-device/baguette helper (Phase 4 scope).
	return fmt.Errorf("ios tap not supported by simctl on this host (needs Phase 4 helper); use launch/open-url")
}

func (Driver) Swipe(ctx context.Context, udid string, x1, y1, x2, y2, ms int) error {
	_, _, _, _, _, _ = ctx, x1, y1, x2, y2, ms
	return fmt.Errorf("ios swipe not supported by simctl on this host (needs Phase 4 helper); use launch/open-url")
}

func (Driver) Type(ctx context.Context, udid, text string) error {
	_, _ = ctx, text
	// No simctl text injection. Documented limitation: use OpenURL / deep links.
	return fmt.Errorf("ios type not supported by simctl on this host (needs Phase 4 helper); use open-url with a deep link")
}

func (Driver) Key(ctx context.Context, udid, code string) error {
	_, _ = ctx, code
	return fmt.Errorf("ios key not supported by simctl on this host (needs Phase 4 helper); use launch/open-url")
}

func (Driver) OpenURL(ctx context.Context, udid, url string) error {
	_, err := xcrun(ctx, "simctl", "openurl", udid, url)
	return err
}

func (Driver) Press(ctx context.Context, udid, button string) error {
	return fmt.Errorf("ios press %q not supported by simctl on this host (needs Phase 4 helper)", button)
}

// Normalize pins the status bar for deterministic screenshots.
func (Driver) Normalize(ctx context.Context, udid string) error {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "status_bar", udid, "override",
		"--time", "9:41",
		"--dataNetwork", "wifi",
		"--wifiMode", "active", "--wifiBars", "3",
		"--cellularMode", "active", "--cellularBars", "4",
		"--batteryState", "charged", "--batteryLevel", "100",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("status_bar override: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (Driver) Screenshot(ctx context.Context, udid, outPath string) error {
	_, err := xcrun(ctx, "simctl", "io", udid, "screenshot", "--type=png", outPath)
	return err
}
