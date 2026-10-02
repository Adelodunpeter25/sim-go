// Package sdk is sim-go's embeddable facade.
//
// This is the product: console's server-go imports this package, and the
// `sim-go` CLI is a thin consumer of it. Drivers live behind the Driver
// interface; callers dispatch by platform string ("ios"|"android") and never
// touch simctl/adb directly.
package sdk

import (
	"context"
	"fmt"
	"sort"

	"github.com/Adelodunpeter25/sim-go/internal/android"
	"github.com/Adelodunpeter25/sim-go/internal/driver"
	"github.com/Adelodunpeter25/sim-go/internal/idb"
	"github.com/Adelodunpeter25/sim-go/internal/ios"
)

// Device is one emulator/simulator, normalized across platforms.
type Device = driver.Device

// Driver talks to one platform's toolchain.
type Driver = driver.Driver

// Client owns one driver per platform.
type Client struct {
	drivers map[string]Driver
	hub     *streamHub
}

// New builds a Client with the built-in drivers.
func New() *Client {
	c := &Client{drivers: map[string]Driver{
		"ios":     ios.Driver{},
		"android": android.Driver{},
	}}
	c.hub = newStreamHub(c.ListAll)
	return c
}

// Close tears down pooled helper processes (idb companions). One-shot
// callers must defer it: macOS has no parent-death signal, so an unclosed
// companion would outlive the process.
func (c *Client) Close() error {
	c.hub.closeAll()
	idb.CloseAll()
	return nil
}

// Driver resolves a platform name or returns a descriptive error.
func (c *Client) Driver(platform string) (Driver, error) {
	d, ok := c.drivers[platform]
	if !ok {
		return nil, fmt.Errorf("unknown platform %q (want ios|android)", platform)
	}
	return d, nil
}

// ListAll returns every device on every platform, sorted for stable output.
func (c *Client) ListAll(ctx context.Context) ([]Device, error) {
	var all []Device
	var errs []error
	for _, d := range c.drivers {
		devs, err := d.List(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d.Name(), err))
			continue
		}
		all = append(all, devs...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Platform != all[j].Platform {
			return all[i].Platform < all[j].Platform
		}
		return all[i].Name < all[j].Name
	})
	if len(all) == 0 && len(errs) > 0 {
		return nil, errs[0]
	}
	return all, nil
}

// IsBooted resolves the device exactly (never aliases) and reports boot state.
func (c *Client) IsBooted(ctx context.Context, platform, id string) (bool, error) {
	d, err := c.Driver(platform)
	if err != nil {
		return false, err
	}
	devs, err := d.List(ctx)
	if err != nil {
		return false, err
	}
	for _, dev := range devs {
		if dev.ID == id || dev.Name == id {
			switch dev.State {
			case "Booted", "device":
				return true, nil
			default:
				return false, nil
			}
		}
	}
	return false, fmt.Errorf("no %s device %q", platform, id)
}

// Thin pass-throughs so callers live in sdk and never import drivers.

func (c *Client) Boot(ctx context.Context, platform, id string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Boot(ctx, id)
}

// BootOptions tunes BootWith. The zero value behaves exactly like Boot.
type BootOptions struct {
	// Slim applies the fixed slim profile once the device has finished
	// booting. If slim fails the device is left running and the error is
	// returned wrapped, so callers can tell boot succeeded.
	Slim bool
}

// BootWith boots a device (blocking until it is ready, as Boot does) and then
// applies opts. Android accepts an AVD name or serial; the live serial is
// resolved after boot because slim needs it.
func (c *Client) BootWith(ctx context.Context, platform, id string, opts BootOptions) error {
	if err := c.Boot(ctx, platform, id); err != nil {
		return err
	}
	if !opts.Slim {
		return nil
	}
	target, err := c.bootedID(ctx, platform, id)
	if err != nil {
		return fmt.Errorf("booted, but slim failed: %w", err)
	}
	if err := c.Slim(ctx, platform, target); err != nil {
		return fmt.Errorf("booted, but slim failed: %w", err)
	}
	return nil
}

// bootedID maps a user-supplied id (UDID, serial, or AVD/device name) to the
// ID of the live booted device the drivers' verbs expect.
func (c *Client) bootedID(ctx context.Context, platform, id string) (string, error) {
	d, err := c.Driver(platform)
	if err != nil {
		return "", err
	}
	devs, err := d.List(ctx)
	if err != nil {
		return "", err
	}
	for _, dev := range devs {
		if (dev.ID == id || dev.Name == id) && (dev.State == "Booted" || dev.State == "device") {
			return dev.ID, nil
		}
	}
	return "", fmt.Errorf("no booted %s device %q", platform, id)
}

func (c *Client) Shutdown(ctx context.Context, platform, id string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Shutdown(ctx, id)
}

func (c *Client) Slim(ctx context.Context, platform, id string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Slim(ctx, id)
}

func (c *Client) Restore(ctx context.Context, platform, id string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Restore(ctx, id)
}

func (c *Client) Launch(ctx context.Context, platform, id, app string) (string, error) {
	d, err := c.Driver(platform)
	if err != nil {
		return "", err
	}
	return d.Launch(ctx, id, app)
}

func (c *Client) Terminate(ctx context.Context, platform, id, app string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Terminate(ctx, id, app)
}

func (c *Client) Install(ctx context.Context, platform, id, appPath string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Install(ctx, id, appPath)
}

func (c *Client) Uninstall(ctx context.Context, platform, id, app string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Uninstall(ctx, id, app)
}

func (c *Client) IsInstalled(ctx context.Context, platform, id, app string) (bool, error) {
	d, err := c.Driver(platform)
	if err != nil {
		return false, err
	}
	return d.IsInstalled(ctx, id, app)
}

func (c *Client) Press(ctx context.Context, platform, id, button string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Press(ctx, id, button)
}

func (c *Client) SetAppearance(ctx context.Context, platform, id, mode string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.SetAppearance(ctx, id, mode)
}

// Appearance returns the device's current UI mode.
func (c *Client) Appearance(ctx context.Context, platform, id string) (string, error) {
	d, err := c.Driver(platform)
	if err != nil {
		return "", err
	}
	return d.Appearance(ctx, id)
}

func (c *Client) Tap(ctx context.Context, platform, id string, x, y int) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Tap(ctx, id, x, y)
}

func (c *Client) Swipe(ctx context.Context, platform, id string, x1, y1, x2, y2, ms int) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Swipe(ctx, id, x1, y1, x2, y2, ms)
}

func (c *Client) Type(ctx context.Context, platform, id, text string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Type(ctx, id, text)
}

func (c *Client) Key(ctx context.Context, platform, id, code string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Key(ctx, id, code)
}

func (c *Client) OpenURL(ctx context.Context, platform, id, url string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.OpenURL(ctx, id, url)
}

func (c *Client) Screenshot(ctx context.Context, platform, id, outPath string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	return d.Screenshot(ctx, id, outPath)
}
