package musicconcepts

import (
	_ "embed"
	"encoding/json"
	"slices"
)

//go:embed acoustic_schema.json
var acousticSchemaJSON []byte

var acousticSchema = loadAcousticSchema()

func loadAcousticSchema() map[string][]string {
	var schema struct {
		Classifiers map[string][]string `json:"classifiers"`
	}
	if err := json.Unmarshal(acousticSchemaJSON, &schema); err != nil {
		panic(err)
	}
	return schema.Classifiers
}

// ValidAcousticClass checks the reviewed legacy archive schema, not newer
// Essentia neural classifiers. Presence is not evidence of musical equivalence.
func ValidAcousticClass(model, label string) bool {
	return slices.Contains(acousticSchema[model], label)
}
