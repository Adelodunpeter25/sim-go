package android

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/driver"
)

// List returns connected devices (adb devices) plus known AVDs
// (emulator -list-avds) that are not currently booted.
func (Driver) List(ctx context.Context) ([]driver.Device, error) {
	var devs []driver.Device
	out, err := adb(ctx, "devices")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(out), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		serial, state := f[0], f[1]
		name := serial
		if strings.HasPrefix(serial, "emulator-") {
			if avd, err := adb(ctx, "-s", serial, "emu", "avd", "name"); err == nil {
				if n := strings.TrimSpace(string(avd)); n != "" {
					// adb shell appends \r; strip it so names compare equal.
					name = strings.TrimSpace(strings.SplitN(n, "\n", 2)[0])
				}
			}
		}
		devs = append(devs, driver.Device{Platform: "android", ID: serial, Name: name, State: state})
	}
	// AVDs known but not booted (SDK dirs first, then PATH).
	if emu := EmulatorBin(); emu != "" {
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
// -no-boot-anim -gpu host -memory 4096, then waits for boot_completed.
// Pass the AVD name (ID with State "avd") or an already-connected serial
// (no-op: returns nil if `adb -s <id> shell getprop sys.boot_completed` is 1).
func (Driver) Boot(ctx context.Context, id string) error {
	if completed(ctx, id) {
		return nil
	}
	emu := EmulatorBin()
	if emu == "" {
		return fmt.Errorf("emulator not found (install Android SDK emulator or set ANDROID_HOME)")
	}
	ram := os.Getenv("SIM_GO_ANDROID_RAM_MB")
	if ram == "" {
		ram = "4096"
	}
	// GPU mode override: -gpu host is fast but breaks the hardware frame path
	// (and with it screenrecord/scrcpy) on some hosts; swiftshader_indirect
	// is the software fallback.
	gpu := os.Getenv("SIM_GO_ANDROID_GPU")
	if gpu == "" {
		gpu = "host"
	}
	args := []string{"-avd", id, "-no-boot-anim", "-no-window", "-gpu", gpu, "-memory", ram, "-no-snapshot"}
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
