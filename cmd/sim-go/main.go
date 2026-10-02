// Command sim-go drives iOS simulators and Android emulators.
//
// Thin consumer of sdk (the embeddable product). Usage:
//
//	sim-go list [-platform ios|android]
//	sim-go doctor
//	sim-go boot|shutdown|slim|restore|normalize <ios|android> <id>
//	sim-go launch <platform> <id> <bundle|package>
//	sim-go terminate|uninstall <platform> <id> <bundle|package>
//	sim-go install <platform> <id> <app.apk|.app>
//	sim-go press <platform> <id> <home|back|lock|power|volume-up|volume-down|menu>
//	sim-go tap <platform> <id> <x> <y>
//	sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]
//	sim-go type <platform> <id> <text...>
//	sim-go key <platform> <id> <code>
//	sim-go open-url <platform> <id> <url>
//	sim-go screenshot <platform> <id> <out.png>
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/idb"
	"github.com/Adelodunpeter25/sim-go/internal/scrcpy"
	"github.com/Adelodunpeter25/sim-go/sdk"
)

const version = "0.1.0"

func usage() {
	fmt.Fprintf(os.Stderr, `sim-go %s — SDK CLI for iOS simulators + Android emulators (mac/linux)

usage:
  sim-go list [-platform ios|android]
  sim-go doctor [-json]
  sim-go boot|shutdown|slim|restore|normalize <ios|android> <id>
  sim-go launch <platform> <id> <bundle|package>
  sim-go terminate|uninstall <platform> <id> <bundle|package>
  sim-go install <platform> <id> <app.apk|.app>
  sim-go press <platform> <id> <button>
  sim-go tap <platform> <id> <x> <y>
  sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]
  sim-go type <platform> <id> <text...>
  sim-go key <platform> <id> <code>
  sim-go open-url <platform> <id> <url>
  sim-go screenshot <platform> <id> <out.png>

env:
  ANDROID_HOME             Android SDK location (adb/emulator discovery)
  SIM_GO_ANDROID_RAM_MB    guest RAM for boot android (default 4096)
  SIM_GO_ANDROID_GPU       emulator GPU: host (default, fast) or swiftshader_indirect
                           (software; needed where host GL starves the video
                           encoder — observed on Intel mac, screenrecord/scrcpy
                           get zero frames with -gpu host)
  SIM_GO_SCRCPY_SERVER     override path to scrcpy-server binary (default:
                           pinned v2.7 auto-downloaded once from GitHub
                           releases, cached; no scrcpy install needed)
`, version)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := sdk.New()

	ok := func(op string, err error) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: error: %v\n", op, err)
			os.Exit(1)
		}
		fmt.Printf("%s: ok\n", op)
	}

	switch os.Args[1] {
	case "-h", "-help", "--help", "help":
		usage()
	case "version", "-v", "--version":
		fmt.Println("sim-go", version)
	case "list":
		fs := flag.NewFlagSet("list", flag.ExitOnError)
		platform := fs.String("platform", "", "ios|android (default: both)")
		_ = fs.Parse(os.Args[2:])
		devs, err := c.ListAll(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list: error: %v\n", err)
			os.Exit(1)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "PLATFORM\tID\tNAME\tSTATE\tOS")
		for _, dev := range devs {
			if *platform != "" && dev.Platform != *platform {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", dev.Platform, dev.ID, dev.Name, dev.State, dev.OS)
		}
		w.Flush()
	case "doctor":
		fs := flag.NewFlagSet("doctor", flag.ExitOnError)
		asJSON := fs.Bool("json", false, "machine-readable output")
		_ = fs.Parse(os.Args[2:])
		d := c.Doctor(ctx)
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(d)
			return
		}
		fmt.Printf("os: %s\nxcode: %v (%s)\nsimctl: %v\nadb: %v\nemulator: %v\ndisk: %.1f GB free (enough: %v)\n%s\n",
			d.OS, d.XcodeInstalled, d.XcodeSelectPath, d.SimctlAvailable,
			d.ADBAvailable, d.EmulatorAvail,
			float64(d.DiskFreeBytes)/(1<<30), d.HasEnoughDiskGB, d.Detail)
	case "boot", "shutdown", "slim", "restore", "normalize":
		needArgs(4, os.Args[1]+" <ios|android> <id>")
		op, platform, id := os.Args[1], os.Args[2], os.Args[3]
		var err error
		switch op {
		case "boot":
			err = c.Boot(ctx, platform, id)
		case "shutdown":
			err = c.Shutdown(ctx, platform, id)
		case "slim":
			err = c.Slim(ctx, platform, id)
		case "restore":
			err = c.Restore(ctx, platform, id)
		case "normalize":
			err = c.Normalize(ctx, platform, id)
		}
		ok(op, err)
	case "launch":
		needArgs(5, "launch <platform> <id> <bundle|package>")
		out, err := c.Launch(ctx, os.Args[2], os.Args[3], os.Args[4])
		if err != nil {
			fmt.Fprintf(os.Stderr, "launch: error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("launch: ok %s\n", out)
	case "terminate", "uninstall":
		needArgs(5, os.Args[1]+" <platform> <id> <bundle|package>")
		var err error
		if os.Args[1] == "terminate" {
			err = c.Terminate(ctx, os.Args[2], os.Args[3], os.Args[4])
		} else {
			err = c.Uninstall(ctx, os.Args[2], os.Args[3], os.Args[4])
		}
		ok(os.Args[1], err)
	case "install":
		needArgs(5, "install <platform> <id> <app.apk|.app>")
		ok("install", c.Install(ctx, os.Args[2], os.Args[3], os.Args[4]))
	case "press":
		needArgs(5, "press <platform> <id> <home|back|lock|power|volume-up|volume-down|menu>")
		ok("press", c.Press(ctx, os.Args[2], os.Args[3], os.Args[4]))
	case "tap":
		needArgs(6, "tap <platform> <id> <x> <y>")
		x, e1 := strconv.Atoi(os.Args[4])
		y, e2 := strconv.Atoi(os.Args[5])
		if e1 != nil || e2 != nil {
			die("x and y must be integers")
		}
		ok("tap", c.Tap(ctx, os.Args[2], os.Args[3], x, y))
	case "swipe":
		if len(os.Args) != 8 && len(os.Args) != 9 {
			die("usage: sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]")
		}
		nums := make([]int, 4)
		for i := 0; i < 4; i++ {
			n, err := strconv.Atoi(os.Args[4+i])
			if err != nil {
				die("coords must be integers")
			}
			nums[i] = n
		}
		ms := 300
		if len(os.Args) == 9 {
			var err error
			ms, err = strconv.Atoi(os.Args[8])
			if err != nil {
				die("ms must be an integer")
			}
		}
		ok("swipe", c.Swipe(ctx, os.Args[2], os.Args[3], nums[0], nums[1], nums[2], nums[3], ms))
	case "type":
		if len(os.Args) < 5 {
			die("usage: sim-go type <platform> <id> <text...>")
		}
		text := join(os.Args[4:])
		ok("type", c.Type(ctx, os.Args[2], os.Args[3], text))
	case "key":
		needArgs(5, "key <platform> <id> <code>")
		ok("key", c.Key(ctx, os.Args[2], os.Args[3], os.Args[4]))
	case "open-url":
		needArgs(5, "open-url <platform> <id> <url>")
		ok("open-url", c.OpenURL(ctx, os.Args[2], os.Args[3], os.Args[4]))
	case "screenshot":
		needArgs(5, "screenshot <platform> <id> <out.png>")
		ok("screenshot", c.Screenshot(ctx, os.Args[2], os.Args[3], os.Args[4]))
	case "stream-probe":
		needArgs(4, "stream-probe android <avd|serial>")
		runStreamProbe(ctx, c, os.Args[2], os.Args[3])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func needArgs(n int, use string) {
	if len(os.Args) != n {
		die("usage: sim-go " + use)
	}
}

// runStreamProbe opens a live scrcpy session, waits for the first key frame
// (proving H.264 flows), taps screen center, and closes. Diagnostic for the
// Phase 4 streaming work; the browser WS multiplex comes later.
func runStreamProbe(ctx context.Context, c *sdk.Client, platform, id string) {
	if platform == "android" {
		runAndroidProbe(ctx, c, id)
		return
	}
	if platform == "ios" {
		runIOSProbe(ctx, id)
		return
	}
	die("stream-probe supports ios|android")
}

func runAndroidProbe(ctx context.Context, c *sdk.Client, id string) {
	serial := id
	if !isSerial(id) {
		devs, err := c.ListAll(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "stream-probe: list: %v\n", err)
			os.Exit(1)
		}
		found := false
		for _, d := range devs {
			if d.Platform == "android" && d.Name == id && isSerial(d.ID) {
				serial = d.ID
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "stream-probe: no booted emulator for AVD %q (boot it first)\n", id)
			os.Exit(1)
		}
	}
	sess, err := scrcpy.Start(ctx, serial)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream-probe: start: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()
	fmt.Printf("stream: %s %dx%d (%s)\n", sess.Meta.Name, sess.Meta.Width, sess.Meta.Height, serial)
	frame, err := sess.WaitKeyframe(30 * time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream-probe: keyframe: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("keyframe: %d bytes H.264\n", len(frame.Payload))
	if err := sess.Tap(sess.Meta.Width/2, sess.Meta.Height/2); err != nil {
		fmt.Fprintf(os.Stderr, "stream-probe: tap: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("tap: ok (center)")
}

func isSerial(id string) bool {
	return len(id) > 9 && id[:9] == "emulator-"
}

// runIOSProbe opens an idb companion session, waits for an IDR frame
// (proving H.264 flows), and taps center in HID points.
func runIOSProbe(ctx context.Context, udid string) {
	sess, err := idb.Start(ctx, udid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream-probe: start: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()
	dims := sess.Desc.GetTargetDescription().GetScreenDimensions()
	fmt.Printf("stream: %s %dx%d px (%dx%d pt)\n",
		sess.Desc.GetTargetDescription().GetName(),
		dims.GetWidth(), dims.GetHeight(), dims.GetWidthPoints(), dims.GetHeightPoints())
	vs, err := sess.StartVideo(30, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream-probe: video: %v\n", err)
		os.Exit(1)
	}
	defer vs.Stop()
	nals, err := waitIDR(vs, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream-probe: keyframe: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("idr: SPS+PPS+IDR seen (%d NALs reassembled)\n", nals)
	sx, sy := sess.Points()
	fmt.Printf("hid scale: %.2f x %.2f px/pt\n", sx, sy)
	cx, cy := float64(dims.GetWidthPoints())/2, float64(dims.GetHeightPoints())/2
	if err := sess.Tap(ctx, cx, cy); err != nil {
		fmt.Fprintf(os.Stderr, "stream-probe: tap: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("tap: ok (%.0f,%.0f pt)\n", cx, cy)
}

// waitIDR accumulates Annex-B payloads until SPS + IDR NALs have been seen.
func waitIDR(vs *idb.VideoStream, timeout time.Duration) (int, error) {
	var buf []byte
	sawSPS, sawIDR, nals := false, false, 0
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case chunk, ok := <-vs.Frames:
			if !ok {
				return 0, fmt.Errorf("video pipe closed")
			}
			buf = append(buf, chunk...)
			for _, nal := range splitAnnexB(buf) {
				if len(nal) == 0 {
					continue
				}
				nals++
				switch nal[0] & 0x1f {
				case 7:
					sawSPS = true
				case 5:
					if sawSPS {
						sawIDR = true
					}
				}
			}
			// Keep only the tail: NALs may split across chunks.
			if len(buf) > 1<<20 {
				buf = buf[len(buf)-(1<<20):]
			}
			if sawIDR {
				return nals, nil
			}
		case <-time.After(2 * time.Second):
		}
	}
	return 0, fmt.Errorf("no IDR within %s", timeout)
}

// splitAnnexB is a local Annex-B splitter (idb payloads, same packing as scrcpy).
func splitAnnexB(b []byte) [][]byte {
	var nals [][]byte
	start := -1
	emit := func(end int) {
		if start >= 0 && end > start {
			nals = append(nals, b[start:end])
		}
	}
	i := 0
	for i < len(b) {
		if i+2 < len(b) && b[i] == 0 && b[i+1] == 0 {
			if b[i+2] == 1 {
				emit(i)
				i += 3
				start = i
				continue
			}
			if i+3 < len(b) && b[i+2] == 0 && b[i+3] == 1 {
				emit(i)
				i += 4
				start = i
				continue
			}
		}
		i++
	}
	emit(len(b))
	return nals
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
