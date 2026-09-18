package core

// MetadataAnnotation is a source claim. It is not a measurement or a calibrated
// model probability. Raw values and conflicts remain available to consumers.
type MetadataAnnotation struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	SourceKey string `json:"sourceKey"`
	Origin    string `json:"origin"`
	Scale     string `json:"scale,omitempty"`
}
