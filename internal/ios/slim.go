package ios

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Adelodunpeter25/sim-go/internal/slim"
)

// Slim disables slim.IOSLabels on the running session (disable + bootout),
// so the daemons stop now and stay off on the next boot (iOS 18.5+).
// Idempotent: already-disabled labels are skipped via print-disabled.
func (Driver) Slim(ctx context.Context, udid string) error {
	current, err := disabled(ctx, udid)
	if err != nil {
		return err
	}
	for _, label := range slim.IOSLabels {
		if current[label] {
			continue
		}
		if _, err := xcrun(ctx, "simctl", "spawn", udid, "launchctl", "disable", "system/"+label); err != nil {
			return err
		}
		out, err := exec.CommandContext(ctx, "xcrun", "simctl", "spawn", udid, "launchctl", "bootout", "system/"+label).CombinedOutput()
		if err != nil {
			// exit 3 = "No such process": already not loaded, which is what we want.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
				continue
			}
			return fmt.Errorf("bootout %s: %w: %s", label, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func (Driver) Restore(ctx context.Context, udid string) error {
	current, err := disabled(ctx, udid)
	if err != nil {
		return err
	}
	for _, label := range slim.IOSLabels {
		if !current[label] {
			continue
		}
		if _, err := xcrun(ctx, "simctl", "spawn", udid, "launchctl", "enable", "system/"+label); err != nil {
			return err
		}
	}
	return nil
}

func disabled(ctx context.Context, udid string) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "spawn", udid, "launchctl", "print-disabled", "system").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("launchctl print-disabled: %w: %s", err, strings.TrimSpace(string(out)))
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		open := strings.IndexByte(line, '"')
		if open < 0 {
			continue
		}
		rest := line[open+1:]
		cq := strings.IndexByte(rest, '"')
		if cq < 0 {
			continue
		}
		label := rest[:cq]
		arrow := strings.Index(rest[cq:], "=>")
		if arrow < 0 {
			continue
		}
		val := strings.TrimSpace(rest[cq+arrow+2:])
		if strings.HasPrefix(val, "disabled") || strings.HasPrefix(val, "true") {
			set[label] = true
		}
	}
	return set, nil
}
