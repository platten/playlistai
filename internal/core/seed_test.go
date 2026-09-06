package core

import (
	"encoding/json"
	"testing"
)

func TestRNGSeedJSONRoundTripFullWidth(t *testing.T) {
	t.Parallel()
	const max = RNGSeed("18446744073709551615")
	raw, err := json.Marshal(struct {
		Seed RNGSeed `json:"seed"`
	}{Seed: max})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"seed":"18446744073709551615"}` {
		t.Fatalf("marshal = %s", raw)
	}
	var decoded struct {
		Seed RNGSeed `json:"seed"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Seed != max {
		t.Fatalf("seed = %q", decoded.Seed)
	}
	value, err := decoded.Seed.Int64()
	if err != nil || uint64(value) != ^uint64(0) {
		t.Fatalf("bits = %x, err=%v", uint64(value), err)
	}
}

func TestRNGSeedLoadsLegacyJSONNumbers(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]RNGSeed{
		`9223372036854775807`: "9223372036854775807",
		`-1`:                  "18446744073709551615",
	} {
		var seed RNGSeed
		if err := json.Unmarshal([]byte(raw), &seed); err != nil {
			t.Fatal(err)
		}
		if seed != want {
			t.Errorf("Unmarshal(%s) = %q, want %q", raw, seed, want)
		}
	}
}
