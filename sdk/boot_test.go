package sdk

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type bootDriver struct {
	devs    []Device
	calls   []string
	slimErr error
	bootErr error
}

func (b *bootDriver) Name() string { return "fake" }
func (b *bootDriver) List(context.Context) ([]Device, error) {
	return b.devs, nil
}
func (b *bootDriver) Boot(_ context.Context, id string) error {
	b.calls = append(b.calls, "boot:"+id)
	return b.bootErr
}
func (b *bootDriver) Slim(_ context.Context, id string) error {
	b.calls = append(b.calls, "slim:"+id)
	return b.slimErr
}

// Driver embeds the interface so unused methods panic if called.
type fullBootDriver struct {
	Driver
	*bootDriver
}

func (f fullBootDriver) Name() string                             { return f.bootDriver.Name() }
func (f fullBootDriver) List(c context.Context) ([]Device, error) { return f.bootDriver.List(c) }
func (f fullBootDriver) Boot(c context.Context, id string) error  { return f.bootDriver.Boot(c, id) }
func (f fullBootDriver) Slim(c context.Context, id string) error  { return f.bootDriver.Slim(c, id) }

func bootClient(b *bootDriver) *Client {
	return &Client{drivers: map[string]Driver{"android": fullBootDriver{bootDriver: b}}}
}

func TestBootWithoutSlimDoesNotSlim(t *testing.T) {
	b := &bootDriver{}
	if err := bootClient(b).BootWith(context.Background(), "android", "Pixel", BootOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(b.calls) != 1 || b.calls[0] != "boot:Pixel" {
		t.Fatalf("calls=%v", b.calls)
	}
}

func TestBootWithSlimResolvesLiveSerial(t *testing.T) {
	b := &bootDriver{devs: []Device{{Platform: "android", ID: "emulator-5554", Name: "Pixel", State: "device"}}}
	if err := bootClient(b).BootWith(context.Background(), "android", "Pixel", BootOptions{Slim: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"boot:Pixel", "slim:emulator-5554"}
	if strings.Join(b.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls=%v want %v", b.calls, want)
	}
}

func TestBootWithSlimFailureKeepsDeviceAndSaysSo(t *testing.T) {
	b := &bootDriver{
		devs:    []Device{{ID: "emulator-5554", Name: "Pixel", State: "device"}},
		slimErr: errors.New("boom"),
	}
	err := bootClient(b).BootWith(context.Background(), "android", "Pixel", BootOptions{Slim: true})
	if err == nil || !strings.Contains(err.Error(), "booted, but slim failed") {
		t.Fatalf("err=%v", err)
	}
}

func TestBootWithBootFailureSkipsSlim(t *testing.T) {
	b := &bootDriver{bootErr: errors.New("nope")}
	if err := bootClient(b).BootWith(context.Background(), "android", "Pixel", BootOptions{Slim: true}); err == nil {
		t.Fatal("want error")
	}
	if len(b.calls) != 1 {
		t.Fatalf("calls=%v", b.calls)
	}
}
