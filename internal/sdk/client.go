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
	"github.com/Adelodunpeter25/sim-go/internal/ios"
)

// Client owns one driver per platform.
type Client struct {
	drivers map[string]driver.Driver
}

// New builds a Client with the built-in drivers.
func New() *Client {
	return &Client{drivers: map[string]driver.Driver{
		"ios":     ios.Driver{},
		"android": android.Driver{},
	}}
}

// Driver resolves a platform name or returns a descriptive error.
func (c *Client) Driver(platform string) (driver.Driver, error) {
	d, ok := c.drivers[platform]
	if !ok {
		return nil, fmt.Errorf("unknown platform %q (want ios|android)", platform)
	}
	return d, nil
}

// ListAll returns every device on every platform, sorted for stable output.
func (c *Client) ListAll(ctx context.Context) ([]driver.Device, error) {
	var all []driver.Device
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
