package core

// DSPValue is an observed preview measurement or an explicit unavailable value.
// Unknown values have nil Value and a machine-readable Reason.
type DSPValue struct {
	Value  *float64           `json:"value"`
	State  FeatureMissingness `json:"state"`
	Reason string             `json:"reason,omitempty"`
}

// DSPFeatures describes measured audio, not inferred whole-track musical traits.
type DSPFeatures struct {
	RMSDBFS              DSPValue `json:"rms_dbfs"`
	SamplePeakDBFS       DSPValue `json:"sample_peak_dbfs"`
	CrestFactorDB        DSPValue `json:"crest_factor_db"`
	RMSWindowSpreadDB    DSPValue `json:"short_window_rms_spread_db"`
	SubbassEnergyRatio   DSPValue `json:"subbass_energy_ratio"`
	BassEnergyRatio      DSPValue `json:"bass_energy_ratio"`
	TrebleEnergyRatio    DSPValue `json:"treble_energy_ratio"`
	SpectralCentroidHz   DSPValue `json:"spectral_centroid_hz"`
	PositiveSpectralFlux DSPValue `json:"positive_spectral_flux"`
	OnsetRateHz          DSPValue `json:"onset_rate_hz"`
}
