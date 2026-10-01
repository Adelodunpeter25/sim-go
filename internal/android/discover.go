package android

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sdkDir returns the first existing Android SDK root, preferring ANDROID_HOME.
func sdkDir(candidates ...string) string {
	if p := os.Getenv("ANDROID_HOME"); p != "" {
		candidates = append([]string{p}, candidates...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, "Library", "Android", "sdk"),
			filepath.Join(home, "Android", "Sdk"),
		)
	}
	for _, base := range candidates {
		if base == "" {
			continue
		}
		if _, err := os.Stat(base); err == nil {
			return base
		}
	}
	return ""
}

func adbPath() string {
	if sdk := sdkDir(); sdk != "" {
		if cand := filepath.Join(sdk, "platform-tools", "adb"); isExec(cand) {
			return cand
		}
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p
	}
	return "adb"
}

// EmulatorBin finds the emulator binary via SDK dirs first, then PATH.
// Exported so sdk.Doctor can report honest availability.
func EmulatorBin() string {
	if sdk := sdkDir(); sdk != "" {
		if cand := filepath.Join(sdk, "emulator", "emulator"); isExec(cand) {
			return cand
		}
	}
	if p, err := exec.LookPath("emulator"); err == nil {
		return p
	}
	return ""
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !fi.IsDir() && fi.Mode()&0o111 != 0
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
		if !isExec(adbPath()) {
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
