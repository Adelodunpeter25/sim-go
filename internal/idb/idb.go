// Package idb drives iOS simulators through Meta's idb_companion v1.1.8
// (pinned: last Intel-capable companion; MIT licensed).
//
// Like internal/scrcpy for Android: we supervise one companion per simulator,
// speak its native protocol (gRPC, generated from the pinned proto), and
// expose HID input + H.264 video. No Python, no Node, no Xcode beyond the
// simctl toolchain the ios driver already needs.
//
// Wire reference: the v1.1.8 checkout at /Users/adelodunpeter/Developer/Projects/idb
// (proto/idb.proto is vendored under proto/ and generated into gen/).
package idb

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Version is the pinned companion release. The gRPC surface in gen/ is
// generated from this version's proto; do not bump one without the other.
const Version = "1.1.8"

func downloadURL() string {
	return "https://github.com/facebook/idb/releases/download/v" + Version + "/idb-companion.universal.tar.gz"
}

// CompanionPath returns the companion binary: explicit override first
// (SIM_GO_IDB_COMPANION, path to the idb_companion executable), else the
// versioned cache layout.
func CompanionPath() string {
	if p := os.Getenv("SIM_GO_IDB_COMPANION"); p != "" {
		return p
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = filepath.Join(os.TempDir(), "sim-go")
	}
	return filepath.Join(cache, "sim-go", "idb", "v"+Version, "idb-companion.universal", "bin", "idb_companion")
}

// CompanionCached reports whether a runnable companion is already on disk
// (override or cache), without touching the network.
func CompanionCached() bool { return isExecFile(CompanionPath()) }

// EnsureCompanion guarantees a runnable companion: override, cache hit, or
// one download + extract (~17MB, cached afterwards).
func EnsureCompanion(ctx context.Context) (string, error) {
	path := CompanionPath()
	if isExecFile(path) {
		return path, nil
	}
	if os.Getenv("SIM_GO_IDB_COMPANION") != "" {
		return "", fmt.Errorf("SIM_GO_IDB_COMPANION=%s not executable", path)
	}
	root := filepath.Join(filepath.Dir(path), "..", "..")
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("idb cache dir: %w", err)
	}
	tmp, err := os.CreateTemp("", "idb-companion-*.tar.gz")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := download(ctx, downloadURL(), tmp); err != nil {
		_ = tmp.Close()
		return "", err
	}
	_ = tmp.Close()
	if err := extractTarGz(tmpName, root); err != nil {
		return "", err
	}
	if !isExecFile(path) {
		return "", fmt.Errorf("idb companion missing after extract (want %s)", path)
	}
	return path, nil
}

func download(ctx context.Context, url string, dst *os.File) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download idb-companion v%s: %w (network needed once, then cached)", Version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download idb-companion v%s: HTTP %s", Version, resp.Status)
	}
	n, err := io.Copy(dst, resp.Body)
	if err != nil {
		return fmt.Errorf("download idb-companion: %w", err)
	}
	if n < 1<<20 {
		return fmt.Errorf("download idb-companion: suspiciously small (%d bytes)", n)
	}
	return nil
}

func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("idb tar: %w", err)
		}
		target := filepath.Join(dst, filepath.Clean("/"+hdr.Name))
		if !isSubpath(dst, target) {
			return fmt.Errorf("idb tar: outside root: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			_ = out.Close()
		}
	}
}

func isSubpath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func isExecFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() || fi.Size() < 1024 {
		return false
	}
	return fi.Mode()&0o111 != 0
}
