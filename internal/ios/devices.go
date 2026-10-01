package ios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/driver"
)

func (Driver) List(ctx context.Context) ([]driver.Device, error) {
	out, err := xcrun(ctx, "simctl", "list", "devices", "-j")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Devices map[string][]struct {
			UDID        string `json:"udid"`
			Name        string `json:"name"`
			State       string `json:"state"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"devices"`
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("parse simctl list: %w", err)
	}
	var devs []driver.Device
	for rt, ds := range parsed.Devices {
		if !strings.Contains(rt, "iOS") {
			continue
		}
		os := runtimeVersion(rt)
		for _, d := range ds {
			if !d.IsAvailable {
				continue
			}
			devs = append(devs, driver.Device{Platform: "ios", ID: d.UDID, Name: d.Name, State: d.State, OS: os})
		}
	}
	return devs, nil
}

func runtimeVersion(rt string) string {
	i := strings.LastIndex(rt, "iOS-")
	if i < 0 {
		return "?"
	}
	return strings.ReplaceAll(rt[i+len("iOS-"):], "-", ".")
}

func (Driver) Boot(ctx context.Context, udid string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "boot", udid).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if !strings.Contains(msg, "already booted") && !strings.Contains(msg, "Booted") {
			return fmt.Errorf("simctl boot: %w: %s", err, msg)
		}
	}
	_, err = xcrun(ctx, "simctl", "bootstatus", udid, "-b")
	return err
}

func (Driver) Shutdown(ctx context.Context, udid string) error {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "shutdown", udid).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Shutdown") {
			return nil
		}
		return fmt.Errorf("simctl shutdown: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
