package android

import (
	"context"
	"strings"
	"time"
)

// Launch starts a package's launcher activity (no activity name needed).
func (d Driver) Launch(ctx context.Context, id, pkg string) (string, error) {
	out, err := adb(ctx, "-s", id, "shell", "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (d Driver) Terminate(ctx context.Context, id, pkg string) error {
	_, err := adb(ctx, "-s", id, "shell", "am", "force-stop", pkg)
	return err
}

func (d Driver) Install(ctx context.Context, id, apkPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	_, err := adb(ctx, "-s", id, "install", "-r", apkPath)
	return err
}

func (d Driver) Uninstall(ctx context.Context, id, pkg string) error {
	_, err := adb(ctx, "-s", id, "uninstall", pkg)
	return err
}

// IsInstalled checks `pm path`: empty output means absent.
func (d Driver) IsInstalled(ctx context.Context, id, pkg string) (bool, error) {
	out, err := adb(ctx, "-s", id, "shell", "pm", "path", pkg)
	if err != nil {
		return false, nil // device unreachable/absent package both read as not installed
	}
	return strings.Contains(string(out), "package:"), nil
}
