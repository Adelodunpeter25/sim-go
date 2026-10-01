// Command sim-go drives iOS simulators and Android emulators.
//
// Usage:
//
//	sim-go list [-platform ios|android]
//	sim-go boot <ios|android> <id>        # UDID for ios, AVD name or serial for android
//	sim-go shutdown|slim|restore <ios|android> <id>
//	sim-go tap <platform> <id> <x> <y>
//	sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]
//	sim-go type <platform> <id> <text...>
//	sim-go key <platform> <id> <code>     # android KEYCODE_* (BACK, HOME, 82); ios: not supported in v1
//	sim-go open-url <platform> <id> <url>
//	sim-go screenshot <platform> <id> <out.png>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/android"
	"github.com/Adelodunpeter25/sim-go/internal/driver"
	"github.com/Adelodunpeter25/sim-go/internal/ios"
)

const version = "0.1.0"

func drivers() map[string]driver.Driver {
	return map[string]driver.Driver{
		"ios":     ios.Driver{},
		"android": android.Driver{},
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `sim-go %s — drive iOS simulators + Android emulators (mac/linux)

usage:
  sim-go list [-platform ios|android]
  sim-go boot|shutdown|slim|restore <ios|android> <id>
  sim-go tap <platform> <id> <x> <y>
  sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]
  sim-go type <platform> <id> <text...>
  sim-go key <platform> <id> <code>
  sim-go open-url <platform> <id> <url>
  sim-go screenshot <platform> <id> <out.png>

env:
  ANDROID_HOME             Android SDK location (adb/emulator discovery)
  SIM_GO_ANDROID_RAM_MB    guest RAM for `+"`sim-go boot android`"+` (default 2048)
`, version)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	switch os.Args[1] {
	case "-h", "-help", "--help", "help":
		usage()
	case "version", "-v", "--version":
		fmt.Println("sim-go", version)
	case "list":
		fs := flag.NewFlagSet("list", flag.ExitOnError)
		platform := fs.String("platform", "", "ios|android (default: both)")
		_ = fs.Parse(os.Args[2:])
		runList(ctx, *platform)
	case "boot", "shutdown", "slim", "restore":
		if len(os.Args) != 4 {
			fmt.Fprintf(os.Stderr, "usage: sim-go %s <ios|android> <id>\n", os.Args[1])
			os.Exit(2)
		}
		runLifecycle(ctx, os.Args[1], os.Args[2], os.Args[3])
	case "tap":
		if len(os.Args) != 6 {
			fmt.Fprintln(os.Stderr, "usage: sim-go tap <platform> <id> <x> <y>")
			os.Exit(2)
		}
		x, e1 := strconv.Atoi(os.Args[4])
		y, e2 := strconv.Atoi(os.Args[5])
		if e1 != nil || e2 != nil {
			fmt.Fprintln(os.Stderr, "x and y must be integers")
			os.Exit(2)
		}
		must(check(ctx, os.Args[2], os.Args[3], "tap", mustDriver(os.Args[2]).Tap(ctx, os.Args[3], x, y)))
	case "swipe":
		if len(os.Args) != 8 && len(os.Args) != 9 {
			fmt.Fprintln(os.Stderr, "usage: sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]")
			os.Exit(2)
		}
		nums := make([]int, 4)
		for i := 0; i < 4; i++ {
			n, err := strconv.Atoi(os.Args[4+i])
			if err != nil {
				fmt.Fprintln(os.Stderr, "coords must be integers")
				os.Exit(2)
			}
			nums[i] = n
		}
		ms := 300
		if len(os.Args) == 9 {
			var err error
			ms, err = strconv.Atoi(os.Args[8])
			if err != nil {
				fmt.Fprintln(os.Stderr, "ms must be an integer")
				os.Exit(2)
			}
		}
		must(check(ctx, os.Args[2], os.Args[3], "swipe", mustDriver(os.Args[2]).Swipe(ctx, os.Args[3], nums[0], nums[1], nums[2], nums[3], ms)))
	case "type":
		if len(os.Args) < 5 {
			fmt.Fprintln(os.Stderr, "usage: sim-go type <platform> <id> <text...>")
			os.Exit(2)
		}
		text := join(os.Args[4:])
		must(check(ctx, os.Args[2], os.Args[3], "type", mustDriver(os.Args[2]).Type(ctx, os.Args[3], text)))
	case "key":
		if len(os.Args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: sim-go key <platform> <id> <code>")
			os.Exit(2)
		}
		must(check(ctx, os.Args[2], os.Args[3], "key", mustDriver(os.Args[2]).Key(ctx, os.Args[3], os.Args[4])))
	case "open-url":
		if len(os.Args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: sim-go open-url <platform> <id> <url>")
			os.Exit(2)
		}
		must(check(ctx, os.Args[2], os.Args[3], "open-url", mustDriver(os.Args[2]).OpenURL(ctx, os.Args[3], os.Args[4])))
	case "screenshot":
		if len(os.Args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: sim-go screenshot <platform> <id> <out.png>")
			os.Exit(2)
		}
		must(check(ctx, os.Args[2], os.Args[3], "screenshot", mustDriver(os.Args[2]).Screenshot(ctx, os.Args[3], os.Args[4])))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func mustDriver(platform string) driver.Driver {
	d, ok := drivers()[platform]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown platform %q (want ios|android)\n", platform)
		os.Exit(2)
	}
	return d
}

// check prints a one-line ok/fail and returns exit-worthy error state.
func check(_ context.Context, _, _ string, op string, err error) error {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: error: %v\n", op, err)
		os.Exit(1)
	}
	fmt.Printf("%s: ok\n", op)
	return nil
}

func must(err error) {
	if err != nil {
		os.Exit(1)
	}
}

func runList(ctx context.Context, only string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PLATFORM\tID\tNAME\tSTATE\tOS")
	for name, d := range drivers() {
		if only != "" && only != name {
			continue
		}
		devs, err := d.List(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s list: %v\n", name, err)
			if only == name {
				os.Exit(1)
			}
			continue
		}
		for _, dev := range devs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", dev.Platform, dev.ID, dev.Name, dev.State, dev.OS)
		}
	}
	w.Flush()
}

func runLifecycle(ctx context.Context, op, platform, id string) {
	d := mustDriver(platform)
	var err error
	switch op {
	case "boot":
		err = d.Boot(ctx, id)
	case "shutdown":
		err = d.Shutdown(ctx, id)
	case "slim":
		err = d.Slim(ctx, id)
	case "restore":
		err = d.Restore(ctx, id)
	}
	must(check(ctx, platform, id, op, err))
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
