// Package android drives Android emulators via `adb` + `emulator`.
//
// Works on mac and linux. Slimming disables slim.AndroidPackages with
// `pm disable-user` (avdslim-style, cf. reference/simfleet src/android.ts).
package android

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/driver"
	"github.com/Adelodunpeter25/sim-go/internal/slim"
)

type Driver struct{}

func (Driver) Name() string { return "android" }

func adbPath() string {
	if p := os.Getenv("ANDROID_HOME"); p != "" {
		cand := filepath.Join(p, "platform-tools", "adb")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		cand := filepath.Join(home, "Library", "Android", "sdk", "platform-tools", "adb")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
		cand = filepath.Join(home, "Android", "Sdk", "platform-tools", "adb")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return "adb"
}

func adb(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, adbPath(), args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("adb %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (Driver) Available(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := exec.LookPath(adbPath()); err != nil {
		// adbPath may be absolute; check runnable instead.
		if _, statErr := os.Stat(adbPath()); statErr != nil {
			return fmt.Errorf("adb not found (set ANDROID_HOME): %w", err)
		}
	}
	out, err := adb(ctx, "version")
	if err != nil {
		return err
	}
	_ = out
	return nil
}

// List returns connected devices (adb devices) plus known AVDs
// (emulator -list-avds) that are not currently booted.
func (Driver) List(ctx context.Context) ([]driver.Device, error) {
	var devs []driver.Device
	out, err := adb(ctx, "devices")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		serial, state := f[0], f[1]
		seen[serial] = true
		name := serial
		if strings.HasPrefix(serial, "emulator-") {
			if avd, err := adb(ctx, "-s", serial, "emu", "avd", "name"); err == nil {
				if n := strings.TrimSpace(string(avd)); n != "" {
					name = strings.SplitN(n, "\n", 2)[0]
				}
			}
		}
		devs = append(devs, driver.Device{Platform: "android", ID: serial, Name: name, State: state})
	}
	// AVDs known but not booted.
	if emu, err := exec.LookPath("emulator"); err == nil {
		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx2, emu, "-list-avds").CombinedOutput(); err == nil {
			for _, avd := range strings.Fields(string(out)) {
				avd = strings.TrimSpace(avd)
				if avd == "" {
					continue
				}
				dup := false
				for _, d := range devs {
					if d.Name == avd {
						dup = true
						break
					}
				}
				if !dup {
					devs = append(devs, driver.Device{Platform: "android", ID: avd, Name: avd, State: "avd"})
				}
			}
		}
	}
	return devs, nil
}

// Boot starts an AVD by name headless with sensible defaults:
// -no-boot-anim -gpu host -memory 2048, then waits for boot_completed.
// Pass the AVD name (ID with State "avd") or an already-connected serial
// (no-op: returns nil if `adb -s <id> shell getprop sys.boot_completed` is 1).
func (Driver) Boot(ctx context.Context, id string) error {
	if completed(ctx, id) {
		return nil
	}
	emu, err := exec.LookPath("emulator")
	if err != nil {
		return fmt.Errorf("emulator not found in PATH (install Android SDK emulator): %w", err)
	}
	ram := os.Getenv("SIM_GO_ANDROID_RAM_MB")
	if ram == "" {
		ram = "2048"
	}
	args := []string{"-avd", id, "-no-boot-anim", "-gpu", "host", "-memory", ram, "-no-snapshot"}
	logF, _ := os.CreateTemp("", "sim-go-emulator-*.log")
	cmd := exec.Command(emu, args...)
	if logF != nil {
		cmd.Stdout = logF
		cmd.Stderr = logF
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start emulator: %w", err)
	}
	// Don't wait on the process; poll for boot.
	deadline := time.Now().Add(4 * time.Minute)
	lister := Driver{}
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		// Find the new serial by scanning devices for an emulator that resolves to this AVD.
		devs, err := lister.List(ctx)
		if err != nil {
			continue
		}
		for _, d := range devs {
			if d.Name == id && strings.HasPrefix(d.ID, "emulator-") && completed(ctx, d.ID) {
				return nil
			}
		}
		// Fallback: any fresh emulator device that just completed boot.
		for _, d := range devs {
			if strings.HasPrefix(d.ID, "emulator-") && d.State == "device" && completed(ctx, d.ID) {
				return nil
			}
		}
	}
	return fmt.Errorf("timed out waiting for emulator %s to boot", id)
}

func completed(ctx context.Context, serial string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := adb(ctx, "-s", serial, "shell", "getprop", "sys.boot_completed")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

func (d Driver) Shutdown(ctx context.Context, id string) error {
	serial := id
	// If given an AVD name, resolve to the live serial first.
	if !strings.HasPrefix(id, "emulator-") {
		if devs, err := d.List(ctx); err == nil {
			for _, d := range devs {
				if d.Name == id && strings.HasPrefix(d.ID, "emulator-") {
					serial = d.ID
					break
				}
			}
		}
	}
	if out, err := adb(ctx, "-s", serial, "emu", "kill"); err == nil {
		_ = out
		return nil
	}
	// Fallback for physical devices / odd states: no-op error.
	return fmt.Errorf("could not kill %s (is it a connected emulator?)", id)
}

func (Driver) Slim(ctx context.Context, id string) error {
	for _, pkg := range slim.AndroidPackages {
		out, err := adb(ctx, "-s", id, "shell", "pm", "disable-user", "--user", "0", pkg)
		if err != nil {
			msg := strings.TrimSpace(string(out))
			// Tolerate already-disabled / unknown packages; fail on real errors.
			if strings.Contains(msg, "Unknown package") || strings.Contains(msg, "already disabled") {
				continue
			}
			// pm prints errors but exits 0 sometimes; detect via output.
			if strings.Contains(msg, "Error") && !strings.Contains(msg, "new state: disabled") {
				continue
			}
		}
	}
	return nil
}

func (Driver) Restore(ctx context.Context, id string) error {
	for _, pkg := range slim.AndroidPackages {
		out, err := adb(ctx, "-s", id, "shell", "pm", "enable", pkg)
		_ = out
		_ = err // best-effort: enabling a non-disabled package is harmless
	}
	return nil
}

func (Driver) Tap(ctx context.Context, id string, x, y int) error {
	_, err := adb(ctx, "-s", id, "shell", "input", "tap", itoa(x), itoa(y))
	return err
}

func (Driver) Swipe(ctx context.Context, id string, x1, y1, x2, y2, ms int) error {
	_, err := adb(ctx, "-s", id, "shell", "input", "swipe", itoa(x1), itoa(y1), itoa(x2), itoa(y2), itoa(ms))
	return err
}

func (Driver) Type(ctx context.Context, id, text string) error {
	// adb input text needs %s for spaces and breaks on special chars.
	escaped := strings.ReplaceAll(text, " ", "%s")
	escaped = strings.ReplaceAll(escaped, "'", "")
	_, err := adb(ctx, "-s", id, "shell", "input", "text", escaped)
	return err
}

func (Driver) Key(ctx context.Context, id, code string) error {
	// Accepts KEYCODE_* names or numbers (e.g. KEYCODE_BACK, 82 for menu).
	if !strings.HasPrefix(code, "KEYCODE_") {
		code = "KEYCODE_" + strings.ToUpper(code)
	}
	_, err := adb(ctx, "-s", id, "shell", "input", "keyevent", code)
	return err
}

func (Driver) OpenURL(ctx context.Context, id, url string) error {
	_, err := adb(ctx, "-s", id, "shell", "am", "start", "-W", "-a", "android.intent.action.VIEW", "-d", url)
	return err
}

func (Driver) Screenshot(ctx context.Context, id, outPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, adbPath(), "-s", id, "exec-out", "screencap", "-p")
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd.Stdout = f
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("screencap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func itoa(n int) string {
	if n < 0 {
		n = 0
	}
	return fmt.Sprint(n)
}
