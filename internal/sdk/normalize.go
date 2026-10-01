package sdk

import (
	"context"
	"fmt"
)

// Normalize makes screenshots deterministic (t3code mobile-showcase pattern):
// fixed status-bar clock/battery on iOS, zeroed animation scales on Android.
// Appearance, locale, and content are left untouched.
func (c *Client) Normalize(ctx context.Context, platform, id string) error {
	d, err := c.Driver(platform)
	if err != nil {
		return err
	}
	if err := d.Normalize(ctx, id); err != nil {
		return fmt.Errorf("normalize %s %s: %w", platform, id, err)
	}
	return nil
}
