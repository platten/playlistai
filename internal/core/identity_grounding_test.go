package core

import "testing"

func TestValidIdentityGroundingProvider(t *testing.T) {
	for _, provider := range []string{"MusicBrainz", "paipack", "MusicBrainz+paipack"} {
		if !ValidIdentityGroundingProvider(provider) {
			t.Errorf("trusted provider rejected: %q", provider)
		}
	}
	for _, provider := range []string{"", "network", "MusicBrainz+network", "+paipack"} {
		if ValidIdentityGroundingProvider(provider) {
			t.Errorf("untrusted provider accepted: %q", provider)
		}
	}
}
