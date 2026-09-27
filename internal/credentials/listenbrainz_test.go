package credentials

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{marker: filepath.Join(t.TempDir(), "connection"), account: "test", get: func(string, string) (string, error) { return "", keyring.ErrNotFound }, set: func(string, string, string) error { return nil }, remove: func(string, string) error { return nil }}
	return s
}
func TestCredentialLifecycle(t *testing.T) {
	s := testStore(t)
	var saved string
	s.set = func(_, _, v string) error { saved = v; return nil }
	s.get = func(string, string) (string, error) { return saved, nil }
	s.remove = func(string, string) error { saved = ""; return nil }
	status, err := s.Connect("private-token")
	if err != nil || !status.Connected || !status.Persistent {
		t.Fatal(status, err)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "private-token") {
		t.Fatal("token leaked into status")
	}
	b, _ := os.ReadFile(s.marker)
	if string(b) != "enabled" {
		t.Fatal("unexpected marker")
	}
	s.token = ""
	s.status = Status{}
	s.load()
	if s.Token() != "private-token" {
		t.Fatal("credential not restored")
	}
	if _, err = s.Connect("replacement"); err != nil || s.Token() != "replacement" {
		t.Fatal(err)
	}
	if status, err = s.Disconnect(); err != nil || status.Connected || saved != "" {
		t.Fatal(status, err)
	}
	s.load()
	if s.Token() != "" {
		t.Fatal("disconnected credential restored")
	}
}
func TestUnavailableStoreAndFailedRemovalNeverRestoreOldToken(t *testing.T) {
	s := testStore(t)
	s.get = func(string, string) (string, error) { return "old-secret", nil }
	if _, err := s.Connect("old-secret"); err != nil {
		t.Fatal(err)
	}
	s.set = func(string, string, string) error { return errors.New("unavailable secret details") }
	s.remove = func(string, string) error { return errors.New("locked") }
	status, err := s.Connect("new-secret")
	if err != nil || !status.Connected || status.Persistent || !status.RemovalPending {
		t.Fatal(status, err)
	}
	s.token = ""
	s.status = Status{}
	s.load()
	if s.Token() != "" {
		t.Fatal("old credential restored after failed replacement")
	}
	status, err = s.Disconnect()
	if err != nil || status.Connected || !status.RemovalPending {
		t.Fatal(status, err)
	}
	s.load()
	if s.Token() != "" {
		t.Fatal("old credential restored after failed removal")
	}
}

func TestDisconnectDisablesWhileSecureStorageIsWaiting(t *testing.T) {
	s := testStore(t)
	if _, err := s.Connect("session-token"); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.remove = func(string, string) error { close(entered); <-release; return nil }
	go func() { defer close(done); _, _ = s.Disconnect() }()
	<-entered
	if s.Token() != "" || s.Status().Connected {
		t.Fatal("credential remains enabled while deletion waits")
	}
	close(release)
	<-done
}
