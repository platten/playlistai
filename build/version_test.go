//go:build production

package build

import "testing"

func TestVersionComesFromConfiguration(t *testing.T) {
	old := configuration
	defer func() { configuration = old }()
	configuration = "info:\n  version: \"1.2.3\"\n"
	if configVersion() != "1.2.3" {
		t.Fatal("version not parsed")
	}
	configuration = "invalid"
	defer func() {
		if recover() == nil {
			t.Fatal("invalid release version accepted")
		}
	}()
	configVersion()
}
