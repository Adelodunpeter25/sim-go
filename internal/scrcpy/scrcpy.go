// Package scrcpy drives Android live video + input through scrcpy's
// device server — without the scrcpy client installed anywhere.
//
// Only the server half is used: a pinned scrcpy-server binary is fetched
// once from GitHub releases and cached locally (Apache-2.0, (c) Genymobile),
// pushed to the device, and spoken to over an adb tunnel:
//
//	device (scrcpy-server, H.264 + control) <-adb-> Session <-Go callers / future WS->
//
// Protocol reference: reference/simfleet/src/android-stream.ts, which runs
// this exact server version. The device server only accepts a client of
// exactly its own version, so Version is pinned, never probed.
package scrcpy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Version is the pinned scrcpy release. Control-message layout and the video
// handshake below match this version exactly; do not bump without
// re-checking control.go and video.go against the release notes.
const Version = "2.7"

func downloadURL() string {
	return "https://github.com/Genymobile/scrcpy/releases/download/v" + Version + "/scrcpy-server-v" + Version
}

// ServerPath returns the local scrcpy-server binary, honouring an explicit
// override first (SIM_GO_SCRCPY_SERVER, cf. simfleet's SCRCPY_SERVER_PATH).
func ServerPath() string {
	if p := os.Getenv("SIM_GO_SCRCPY_SERVER"); p != "" {
		return p
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = filepath.Join(os.TempDir(), "sim-go")
	}
	return filepath.Join(cache, "sim-go", "scrcpy", "v"+Version, "scrcpy-server")
}

// EnsureServer guarantees a usable server binary exists locally: override,
// cache hit, or one download. No scrcpy installation is ever required.
func EnsureServer(ctx context.Context) (string, error) {
	path := ServerPath()
	if isExecFile(path) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("scrcpy cache dir: %w", err)
	}
	tmp := path + ".download"
	if err := download(ctx, downloadURL(), tmp); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", fmt.Errorf("scrcpy chmod: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("scrcpy install: %w", err)
	}
	return path, nil
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download scrcpy-server v%s: %w (network needed once, then cached)", Version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download scrcpy-server v%s: HTTP %s", Version, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("download scrcpy-server: %w", err)
	}
	if closeErr != nil {
		return closeErr
	}
	if n < 1024 {
		return fmt.Errorf("download scrcpy-server: suspiciously small (%d bytes)", n)
	}
	return nil
}

func isExecFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() || fi.Size() < 1024 {
		return false
	}
	return fi.Mode()&0o111 != 0
}
