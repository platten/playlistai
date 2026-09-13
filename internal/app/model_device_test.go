package app

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/intent/llama"
)

func TestPreferredLlamaDevicePrefersNVIDIAThenAvailableMemory(t *testing.T) {
	devices := []llama.Device{
		{ID: "Vulkan0", Name: "AMD Radeon", TotalBytes: 24 << 30, FreeBytes: 20 << 30},
		{ID: "CUDA0", Name: "NVIDIA A", TotalBytes: 12 << 30, FreeBytes: 7 << 30},
		{ID: "CUDA1", Name: "NVIDIA B", TotalBytes: 8 << 30, FreeBytes: 8 << 30},
	}
	got, ok := PreferredLlamaDevice(devices)
	if !ok || got.ID != "CUDA1" {
		t.Fatalf("preferred device = %+v, %v", got, ok)
	}
}

func TestSetModelDevicePersistsChoiceAndUsesItForModelStartup(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.deviceProber = func(context.Context) []llama.Device {
		return []llama.Device{{ID: "CUDA0", Name: "NVIDIA Test", TotalBytes: 8 << 30, FreeBytes: 7 << 30}}
	}
	if err := c.SetModelDevice(context.Background(), "CUDA0"); err != nil {
		t.Fatal(err)
	}
	prefs := config.LoadPrefs(c.cfg.DataDir)
	if prefs.ModelDevice != "CUDA0" {
		t.Fatalf("saved device = %q", prefs.ModelDevice)
	}
	var options llama.Options
	c.modelFactory = func(_ context.Context, got llama.Options) (managedParser, error) {
		options = got
		return &managedFixture{}, nil
	}
	if err := c.SetModel(context.Background(), modelFixture(t), "test"); err != nil {
		t.Fatal(err)
	}
	if options.Device != "CUDA0" || options.GPULayers < 0 {
		t.Fatalf("GPU options = %+v", options)
	}
	if err := c.SetModelDevice(context.Background(), "cpu"); err != nil {
		t.Fatal(err)
	}
	if options.Device != "" || options.GPULayers != -1 {
		t.Fatalf("CPU options = %+v", options)
	}
	if err := c.SetModelDevice(context.Background(), "missing"); err == nil {
		t.Fatal("unavailable GPU accepted")
	}
}
