package sdk

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"github.com/Adelodunpeter25/sim-go/internal/android"
	"github.com/Adelodunpeter25/sim-go/internal/idb"
)

// Diagnostics mirrors what console's device service needs before touching a
// device: which toolchains exist, plus free disk for emulator images.
type Diagnostics struct {
	OS              string `json:"os"`
	XcodeInstalled  bool   `json:"xcodeInstalled"`
	SimctlAvailable bool   `json:"simctlAvailable"`
	ADBAvailable    bool   `json:"adbAvailable"`
	EmulatorAvail   bool   `json:"emulatorAvailable"`
	// IDBCompanionAvailable is true when the pinned idb_companion is already
	// cached; false means the first iOS input/stream will download it once.
	IDBCompanionAvailable bool   `json:"idbCompanionAvailable"`
	DiskFreeBytes         uint64 `json:"diskFreeBytes"`
	HasEnoughDiskGB       bool   `json:"hasEnoughDisk"`
	IOSUsable             bool   `json:"iosUsable"`
	AndroidUsable         bool   `json:"androidUsable"`
	XcodeSelectPath       string `json:"xcodeSelectPath,omitempty"`
	Detail                string `json:"detail,omitempty"`
}

// Doctor probes the host. It never fails: missing tools read as false flags,
// and Detail carries the human-readable summary.
func (c *Client) Doctor(ctx context.Context) Diagnostics {
	d := Diagnostics{OS: runtime.GOOS}
	if runtime.GOOS == "darwin" {
		if out, err := exec.CommandContext(ctx, "xcode-select", "-p").Output(); err == nil {
			d.XcodeSelectPath = strings.TrimSpace(string(out))
			d.XcodeInstalled = strings.Contains(d.XcodeSelectPath, "Xcode.app")
		}
		d.IDBCompanionAvailable = idb.CompanionCached()
		if err := c.drivers["ios"].Available(ctx); err == nil {
			d.SimctlAvailable = true
		}
	}
	if err := c.drivers["android"].Available(ctx); err == nil {
		d.ADBAvailable = true
	}
	if android.EmulatorBin() != "" {
		d.EmulatorAvail = true
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(homeOrRoot(), &fs); err == nil {
		d.DiskFreeBytes = fs.Bavail * uint64(fs.Bsize)
		d.HasEnoughDiskGB = d.DiskFreeBytes >= 10<<30
	}
	d.IOSUsable = d.XcodeInstalled && d.SimctlAvailable
	d.AndroidUsable = d.ADBAvailable
	d.Detail = summarize(d)
	return d
}

func homeOrRoot() string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		return "/"
	}
	return "."
}

func summarize(d Diagnostics) string {
	var parts []string
	if d.IOSUsable {
		parts = append(parts, "ios: ready")
	} else {
		parts = append(parts, "ios: unavailable (needs Xcode + simctl on macOS)")
	}
	if d.AndroidUsable {
		if d.EmulatorAvail {
			parts = append(parts, "android: ready (adb+emulator)")
		} else {
			parts = append(parts, "android: adb only (no emulator binary)")
		}
	} else {
		parts = append(parts, "android: unavailable (needs adb, set ANDROID_HOME)")
	}
	parts = append(parts, diskNote(d))
	return strings.Join(parts, "; ")
}

func diskNote(d Diagnostics) string {
	if d.DiskFreeBytes == 0 {
		return "disk: unknown"
	}
	gb := float64(d.DiskFreeBytes) / (1 << 30)
	if d.HasEnoughDiskGB {
		return fmt.Sprintf("disk: %.0f GB free", gb)
	}
	return fmt.Sprintf("disk: only %.0f GB free (want >= 10)", gb)
}
