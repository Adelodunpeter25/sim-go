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

	Tap(ctx context.Context, id string, x, y int) error
	Swipe(ctx context.Context, id string, x1, y1, x2, y2, ms int) error
	Type(ctx context.Context, id, text string) error
	Key(ctx context.Context, id, code string) error
	OpenURL(ctx context.Context, id, url string) error
	Screenshot(ctx context.Context, id, outPath string) error
}
