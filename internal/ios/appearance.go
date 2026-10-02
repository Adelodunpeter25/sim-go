package ios

import (
	"context"
	"fmt"
	"strings"
)

// SetAppearance switches the simulator between dark and light mode.
func (Driver) SetAppearance(ctx context.Context, udid, mode string) error {
	mode = strings.ToLower(mode)
	if mode != "dark" && mode != "light" {
		return fmt.Errorf("unknown appearance %q (want dark|light)", mode)
	}
	_, err := xcrun(ctx, "simctl", "ui", udid, "appearance", mode)
	return err
}
