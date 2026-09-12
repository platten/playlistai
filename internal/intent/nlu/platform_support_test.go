package nlu

import "testing"

func TestMacOSRuntimeMinimumVersion(t *testing.T) {
	for _, version := range []string{"14", "14.0", "14.0.0", "14.7.6", "15.5.0", "26.0", " 14.0.0\n"} {
		if err := checkMacOSVersion(version); err != nil {
			t.Errorf("supported version %q: %v", version, err)
		}
	}
	for _, version := range []string{"10.15.7", "11.7.10", "12.0.0", "12.7.6", "13.7.6"} {
		want := "nlu: compact intent models require macOS 14 or newer; this Mac runs macOS " + version
		if err := checkMacOSVersion(version); err == nil || err.Error() != want {
			t.Errorf("unsupported version %q: got %v, want %q", version, err, want)
		}
	}
}

func TestUnknownMacOSVersionDoesNotClaimRuntimeSupport(t *testing.T) {
	for _, version := range []string{"", " ", "Darwin 23.0", "14.", ".14", "14..0", "14.0.0.1", "14.beta", "-14", "+14", "14.0\x00", "9999999999999999999999999"} {
		want := "nlu: cannot determine macOS version; compact intent models require macOS 14 or newer"
		if err := checkMacOSVersion(version); err == nil || err.Error() != want {
			t.Errorf("malformed version %q: got %v, want %q", version, err, want)
		}
	}
}
