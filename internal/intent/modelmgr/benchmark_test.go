package modelmgr

import "testing"

// BenchmarkRecommendationsByHardware exercises the same capacity policy used
// by the first-run wizard. It needs no physical GPU: the profiles make the
// inexpensive filtering step comparable in CI and on developer machines. It
// is not an inference-throughput benchmark.
func BenchmarkRecommendationsByHardware(b *testing.B) {
	const reserve = int64(1 << 30)
	profiles := []struct {
		name string
		hw   Hardware
	}{
		{name: "cpu"},
		{name: "gpu-4gib", hw: Hardware{GPUAvailable: true, TotalVRAMBytes: 4 << 30, AvailableVRAMBytes: 4 << 30, ReserveBytes: reserve}},
		{name: "rtx-5060-laptop-observed", hw: Hardware{GPUAvailable: true, TotalVRAMBytes: 8123 << 20, AvailableVRAMBytes: 7033 << 20, ReserveBytes: reserve}},
		{name: "rtx-5070-laptop-8gib", hw: Hardware{GPUAvailable: true, TotalVRAMBytes: 8 << 30, AvailableVRAMBytes: 8 << 30, ReserveBytes: reserve}},
		{name: "rtx-5070-desktop-12gib", hw: Hardware{GPUAvailable: true, TotalVRAMBytes: 12 << 30, AvailableVRAMBytes: 12 << 30, ReserveBytes: reserve}},
		{name: "gpu-16gib", hw: Hardware{GPUAvailable: true, TotalVRAMBytes: 16 << 30, AvailableVRAMBytes: 16 << 30, ReserveBytes: reserve}},
		{name: "rtx-3090-desktop-24gib", hw: Hardware{GPUAvailable: true, TotalVRAMBytes: 24 << 30, AvailableVRAMBytes: 24 << 30, ReserveBytes: reserve}},
		{name: "rtx-5090-laptop-24gib", hw: Hardware{GPUAvailable: true, TotalVRAMBytes: 24 << 30, AvailableVRAMBytes: 24 << 30, ReserveBytes: reserve}},
		{name: "rtx-5090-desktop-32gib", hw: Hardware{GPUAvailable: true, TotalVRAMBytes: 32 << 30, AvailableVRAMBytes: 32 << 30, ReserveBytes: reserve}},
	}
	models := Catalog()
	for _, profile := range profiles {
		b.Run(profile.name, func(b *testing.B) {
			fit := Recommendations(models, profile.hw)
			b.ReportAllocs()
			b.ResetTimer()
			b.ReportMetric(float64(len(fit)), "models-fitting")
			for range b.N {
				_ = Recommendations(models, profile.hw)
			}
		})
	}
}
