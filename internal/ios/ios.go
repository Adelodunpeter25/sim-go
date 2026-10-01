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
	"strconv"
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
	_, err := xcrun(ctx, "simctl", "io", udid, "tap", strconv.Itoa(x), strconv.Itoa(y))
	return err
}

func (Driver) Swipe(ctx context.Context, udid string, x1, y1, x2, y2, ms int) error {
	dur := fmt.Sprintf("%.2f", float64(ms)/1000.0)
	_ = dur
	// simctl io swipe has no duration flag; duration is ignored on ios.
	_, err := xcrun(ctx, "simctl", "io", udid, "swipe",
		strconv.Itoa(x1), strconv.Itoa(y1), strconv.Itoa(x2), strconv.Itoa(y2))
	return err
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

func (Driver) Screenshot(ctx context.Context, udid, outPath string) error {
	_, err := xcrun(ctx, "simctl", "io", udid, "screenshot", "--type=png", outPath)
	return err
}
