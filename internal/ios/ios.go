// Package ios drives iOS simulators via `xcrun simctl`.
//
// macOS only: every method returns an error on linux where xcrun does not
// exist. Slimming uses the fixed profile in internal/slim (launchd disable +
// bootout on the live boot session, i.e. simslim's `--no-reboot` path).
package ios

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Driver talks to the Xcode simctl toolchain.
type Driver struct{}

func (Driver) Name() string { return "ios" }

func (Driver) Available(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("ios driver requires macOS (xcrun simctl)")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		return fmt.Errorf("xcrun not found: install Xcode: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "devices", "-j").CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl not usable: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func xcrun(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "xcrun", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("xcrun %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
