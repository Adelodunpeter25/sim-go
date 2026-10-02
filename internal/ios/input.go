package ios

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Adelodunpeter25/sim-go/internal/idb"
)

// Tap/Swipe/Type/Key/Press go through a pooled idb companion (simctl has no
// HID). Coordinates arrive in video pixels, the same contract as Android, and
// are converted to device points here.

// withSession leases the pooled companion for the UDID for one call.
func withSession(ctx context.Context, udid string, fn func(*idb.Session) error) error {
	lease, err := idb.DefaultPool.Acquire(ctx, udid)
	if err != nil {
		return err
	}
	defer lease.Release()
	return fn(lease.Session)
}

func (Driver) Tap(ctx context.Context, udid string, x, y int) error {
	return withSession(ctx, udid, func(s *idb.Session) error {
		sx, sy := s.Points()
		return s.Tap(ctx, float64(x)/sx, float64(y)/sy)
	})
}

func (Driver) Swipe(ctx context.Context, udid string, x1, y1, x2, y2, ms int) error {
	return withSession(ctx, udid, func(s *idb.Session) error {
		sx, sy := s.Points()
		return s.Swipe(ctx, float64(x1)/sx, float64(y1)/sy, float64(x2)/sx, float64(y2)/sy, float64(ms)/1000)
	})
}

func (Driver) Type(ctx context.Context, udid, text string) error {
	return withSession(ctx, udid, func(s *idb.Session) error { return s.Text(ctx, text) })
}

// keyNames lets Key accept the Android KEYCODE_* names the CLI documents.
var keyNames = map[string]int{
	"KEYCODE_HOME": 3, "KEYCODE_BACK": 4, "KEYCODE_ESCAPE": 4,
	"KEYCODE_ENTER": 66, "KEYCODE_DEL": 67,
}

// Key accepts an Android keycode number or KEYCODE_* name. Codes with no iOS
// mapping fail loudly instead of being guessed.
func (Driver) Key(ctx context.Context, udid, code string) error {
	n, err := strconv.Atoi(code)
	if err != nil {
		var ok bool
		if n, ok = keyNames[strings.ToUpper(code)]; !ok {
			return fmt.Errorf("ios key %q unsupported (want 3 HOME, 4 Esc, 66 Enter, 67 Del or the KEYCODE_ names)", code)
		}
	}
	return withSession(ctx, udid, func(s *idb.Session) error {
		handled, err := s.AndroidKey(ctx, n)
		if err == nil && !handled {
			return fmt.Errorf("ios key %d unsupported (want 3 HOME, 4 Esc, 66 Enter, 67 Del)", n)
		}
		return err
	})
}

func (Driver) OpenURL(ctx context.Context, udid, url string) error {
	_, err := xcrun(ctx, "simctl", "openurl", udid, url)
	return err
}

// pressButtons maps Driver buttons to idb HID buttons. back/menu/volume have
// no HID button in the companion proto, so they are rejected, not emulated.
var pressButtons = map[string]string{
	"home": "HOME", "lock": "LOCK", "power": "LOCK",
	"side": "SIDE_BUTTON", "siri": "SIRI",
}

func (Driver) Press(ctx context.Context, udid, button string) error {
	b := strings.ToLower(button)
	name, ok := pressButtons[b]
	if !ok {
		return fmt.Errorf("ios press %q not supported (want home|lock|power|side|siri)", button)
	}
	return withSession(ctx, udid, func(s *idb.Session) error { return s.Button(ctx, name) })
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
