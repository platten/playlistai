package audio

import (
	"os"
	"testing"
)

func TestResolveMERTDeviceRequiresMatchingVerifiedBundle(t *testing.T) {
	for _, test := range []struct {
		requested, backend, want string
		ok                       bool
	}{
		{"auto", "cpu", "cpu", true}, {"auto", "cuda", "cuda:0", true},
		{"cuda", "cuda", "cuda:0", true}, {"CUDA:2", "cuda", "cuda:2", true},
		{"cuda", "cpu", "", false}, {"cpu", "cuda", "", false}, {"cuda:-1", "cuda", "", false},
	} {
		got, err := ResolveMERTDevice(test.requested, test.backend)
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("ResolveMERTDevice(%q,%q)=%q,%v want %q ok=%v", test.requested, test.backend, got, err, test.want, test.ok)
		}
	}
}

func TestParseMERTDevicePreference(t *testing.T) {
	for _, test := range []struct{ input, normalized, preference string }{
		{"", "auto", "auto"}, {"cpu", "cpu", "cpu"}, {"CUDA", "cuda:0", "cuda"}, {"cuda:3", "cuda:3", "cuda"},
	} {
		normalized, preference, err := ParseMERTDevicePreference(test.input)
		if err != nil || normalized != test.normalized || preference != test.preference {
			t.Fatalf("ParseMERTDevicePreference(%q)=%q,%q,%v", test.input, normalized, preference, err)
		}
	}
}

func TestPrependMERTEnvironmentPathReplacesInheritedValue(t *testing.T) {
	got := prependMERTEnvironmentPath([]string{"KEEP=value", "Path=first", "PATH=second"}, "PATH", "bundle", true)
	want := "PATH=bundle" + string(os.PathListSeparator) + "first"
	if len(got) != 2 || got[0] != "KEEP=value" || got[1] != want {
		t.Fatalf("environment = %#v, want KEEP and %q", got, want)
	}
}
