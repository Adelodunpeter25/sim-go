package android

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// pressMap normalizes Console's interact verbs to KEYCODE_* names.
var pressMap = map[string]string{
	"home":        "KEYCODE_HOME",
	"back":        "KEYCODE_BACK",
	"lock":        "KEYCODE_POWER",
	"power":       "KEYCODE_POWER",
	"volume-up":   "KEYCODE_VOLUME_UP",
	"volume-down": "KEYCODE_VOLUME_DOWN",
	"menu":        "KEYCODE_MENU",
}

func (d Driver) Press(ctx context.Context, id, button string) error {
	code, ok := pressMap[strings.ToLower(button)]
	if !ok {
		return fmt.Errorf("unknown button %q (want home|back|lock|power|volume-up|volume-down|menu)", button)
	}
	_, err := adb(ctx, "-s", id, "shell", "input", "keyevent", code)
	return err
}

// Normalize zeroes animation scales so screenshots and agent waits are stable.
func (d Driver) Normalize(ctx context.Context, id string) error {
	for _, kv := range [][2]string{
		{"window_animation_scale", "0"},
		{"transition_animation_scale", "0"},
		{"animator_duration_scale", "0"},
	} {
		if _, err := adb(ctx, "-s", id, "shell", "settings", "put", "global", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func (d Driver) Tap(ctx context.Context, id string, x, y int) error {
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
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.CommandContext(ctx, adbPath(), "-s", id, "exec-out", "screencap", "-p")
	cmd.Stdout = f
	var serr bytes.Buffer
	cmd.Stderr = &serr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("screencap: %w: %s", err, strings.TrimSpace(serr.String()))
	}
	return nil
}

func itoa(n int) string {
	if n < 0 {
		n = 0
	}
	return fmt.Sprint(n)
}
