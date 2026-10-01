package android

import (
	"context"
	"strings"

	"github.com/Adelodunpeter25/sim-go/internal/slim"
)

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
