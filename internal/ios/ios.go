// Package ios drives iOS simulators via `xcrun simctl`.
//
// macOS only: every method returns an error on linux where xcrun does not
// exist. Slimming uses the fixed profile in internal/slim (launchd disable +
// bootout on the live boot session, i.e. simslim's `--no-reboot` path).
package ios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/driver"
	"github.com/Adelodunpeter25/sim-go/internal/slim"
)

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

func (Driver) List(ctx context.Context) ([]driver.Device, error) {
	out, err := xcrun(ctx, "simctl", "list", "devices", "-j")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Devices map[string][]struct {
			UDID        string `json:"udid"`
			Name        string `json:"name"`
			State       string `json:"state"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"devices"`
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("parse simctl list: %w", err)
	}
	var devs []driver.Device
	for rt, ds := range parsed.Devices {
		if !strings.Contains(rt, "iOS") {
			continue
		}
		os := runtimeVersion(rt)
		for _, d := range ds {
			if !d.IsAvailable {
				continue
			}
			devs = append(devs, driver.Device{Platform: "ios", ID: d.UDID, Name: d.Name, State: d.State, OS: os})
		}
	}
	return devs, nil
}

func runtimeVersion(rt string) string {
	i := strings.LastIndex(rt, "iOS-")
	if i < 0 {
		return "?"
	}
	return strings.ReplaceAll(rt[i+len("iOS-"):], "-", ".")
}

func (Driver) Boot(ctx context.Context, udid string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "boot", udid).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if !strings.Contains(msg, "already booted") && !strings.Contains(msg, "Booted") {
			return fmt.Errorf("simctl boot: %w: %s", err, msg)
		}
	}
	_, err = xcrun(ctx, "simctl", "bootstatus", udid, "-b")
	return err
}

func (Driver) Shutdown(ctx context.Context, udid string) error {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "shutdown", udid).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Shutdown") {
			return nil
		}
		return fmt.Errorf("simctl shutdown: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

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
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 3 {
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

func (Driver) Type(ctx context.Context, udid, text string) error {
	// No simctl text injection; send keystrokes via AppleScript-free fallback:
	// boot a tiny keyboard through `spawn` is out of scope for v1.
	// Documented limitation: use OpenURL / deep links for text-heavy flows.
	return fmt.Errorf("ios type not implemented in v1 (simctl has no text injection); use open-url with a deep link")
}

func (Driver) Key(ctx context.Context, udid, code string) error {
	return fmt.Errorf("ios key not implemented in v1 (needs baguette/argent helper like simfleet); tap/swipe/open-url supported")
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
