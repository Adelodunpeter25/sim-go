package driver

import "context"

// Device is one emulator/simulator, normalized across platforms.
type Device struct {
	Platform string `json:"platform"` // "ios" or "android"
	ID       string `json:"id"`       // UDID (ios) or serial/AVD (android)
	Name     string `json:"name"`
	State    string `json:"state"` // "Booted"/"Shutdown" (ios) or "device"/"offline"/"avd" (android)
	OS       string `json:"os,omitempty"`
}

// Driver talks to one platform's toolchain.
// Implementations must be safe for mac+linux; ios returns
// an error on linux where xcrun/simctl does not exist.
type Driver interface {
	Name() string
	// Available reports whether the toolchain exists (xcrun, adb, ...).
	Available(ctx context.Context) error
	List(ctx context.Context) ([]Device, error)
	Boot(ctx context.Context, id string) error
	Shutdown(ctx context.Context, id string) error
	// Slim disables background bloat (fixed profile, see internal/slim).
	Slim(ctx context.Context, id string) error
	// Restore re-enables what Slim disabled.
	Restore(ctx context.Context, id string) error

	// Launch starts an app (iOS bundle ID, Android package) and returns
	// the process/pid line reported by the platform tool.
	Launch(ctx context.Context, id, app string) (string, error)
	// Terminate stops an app.
	Terminate(ctx context.Context, id, app string) error
	// Install sideloads an app package (.app dir for ios, .apk for android).
	Install(ctx context.Context, id, appPath string) error
	// Uninstall removes an app.
	Uninstall(ctx context.Context, id, app string) error
	// IsInstalled reports whether an app is present.
	IsInstalled(ctx context.Context, id, app string) (bool, error)

	// Press is a unified hardware/software button: home, back, lock, power,
	// volume-up, volume-down, menu, app-switcher (Android only). Platforms without a mapping return an
	// explicit unsupported error instead of guessing.
	Press(ctx context.Context, id, button string) error

	// Normalize makes screenshots deterministic: fixed status bar on iOS,
	// zeroed animation scales on Android. Appearance and content untouched.
	Normalize(ctx context.Context, id string) error

	// SetAppearance switches the UI between "dark" and "light".
	SetAppearance(ctx context.Context, id, mode string) error

	// Appearance reports the current mode: "dark" or "light". Android may
	// instead return "auto", "custom_schedule" or "custom_bedtime" when the
	// device follows a schedule.
	Appearance(ctx context.Context, id string) (string, error)

	Tap(ctx context.Context, id string, x, y int) error
	Swipe(ctx context.Context, id string, x1, y1, x2, y2, ms int) error
	Type(ctx context.Context, id, text string) error
	Key(ctx context.Context, id, code string) error
	OpenURL(ctx context.Context, id, url string) error
	Screenshot(ctx context.Context, id, outPath string) error
}
