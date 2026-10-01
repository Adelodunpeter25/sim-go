package ios

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Launch starts an app by bundle ID, terminating any running instance first
// so the call is idempotent. Returns simctl's "<bundle>: <pid>" line.
func (Driver) Launch(ctx context.Context, udid, bundle string) (string, error) {
	out, err := xcrun(ctx, "simctl", "launch", "--terminate-running-process", udid, bundle)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (Driver) Terminate(ctx context.Context, udid, bundle string) error {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "terminate", udid, bundle).CombinedOutput()
	if err != nil {
		// "found nothing to terminate" is already the desired state.
		msg := string(out)
		if strings.Contains(msg, "found nothing to terminate") || strings.Contains(msg, "not running") || strings.Contains(msg, "Invalid device state") {
			return nil
		}
		return fmt.Errorf("simctl terminate: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (Driver) Install(ctx context.Context, udid, appPath string) error {
	_, err := xcrun(ctx, "simctl", "install", udid, appPath)
	return err
}

func (Driver) Uninstall(ctx context.Context, udid, bundle string) error {
	_, err := xcrun(ctx, "simctl", "uninstall", udid, bundle)
	return err
}

// IsInstalled queries one bundle via `simctl listapps <udid> <bundle>`:
// simctl exits non-zero / prints nothing when the bundle is absent.
func (Driver) IsInstalled(ctx context.Context, udid, bundle string) (bool, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "listapps", udid, bundle).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "not installed") || strings.TrimSpace(string(out)) == "" {
			return false, nil
		}
		// listapps exits 0 with "{}" for unknown bundles on some runtimes.
		if strings.TrimSpace(string(out)) == "{}" {
			return false, nil
		}
		return false, fmt.Errorf("simctl listapps: %w: %s", err, strings.TrimSpace(string(out)))
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "{}" {
		return false, nil
	}
	return strings.Contains(trimmed, bundle), nil
}
