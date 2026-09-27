// Package credentials keeps secrets outside preferences, histories and exports.
package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

const service = "org.playlistai.ListenBrainz"

type Status struct {
	Connected      bool `json:"connected"`
	Persistent     bool `json:"persistent"`
	RemovalPending bool `json:"removalPending"`
}

type Store struct {
	mu       sync.Mutex
	changeMu sync.Mutex
	token    string
	status   Status
	marker   string
	account  string
	get      func(string, string) (string, error)
	set      func(string, string, string) error
	remove   func(string, string) error
}

// New scopes credentials to this application profile. The non-secret marker
// prevents a failed deletion or replacement resurrecting an old token at restart.
func New(dataDir string) *Store {
	s := &Store{marker: filepath.Join(dataDir, "listenbrainz-connection"), account: filepath.Clean(dataDir), get: keyring.Get, set: keyring.Set, remove: keyring.Delete}
	s.load()
	return s
}
func (s *Store) load() {
	b, err := os.ReadFile(s.marker)
	if string(b) == "disabled-removal-pending" {
		s.status.RemovalPending = true
	}
	if err != nil || string(b) != "enabled" {
		return
	}
	token, err := s.get(service, s.account)
	if err == nil && token != "" {
		s.token = token
		s.status = Status{Connected: true, Persistent: true}
	}
}
func (s *Store) Status() Status { s.mu.Lock(); defer s.mu.Unlock(); return s.status }
func (s *Store) Token() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.token }
func (s *Store) markerState(value string) error {
	// Atomic rename ensures readers never observe a partially written state.
	f, err := os.CreateTemp(filepath.Dir(s.marker), ".listenbrainz-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(value); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.marker)
}

// Connect accepts only an already validated token. No backend errors can reveal
// credentials; unavailable secure storage yields an explicit session connection.
func (s *Store) Connect(token string) (Status, error) {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	if token == "" {
		return s.Status(), errors.New("ListenBrainz token required")
	}
	if err := s.markerState("disabled"); err != nil {
		return s.Status(), errors.New("could not save ListenBrainz connection state")
	}
	status := Status{Connected: true}
	if err := s.set(service, s.account, token); err == nil {
		if err = s.markerState("enabled"); err == nil {
			status.Persistent = true
		} else {
			status.RemovalPending = s.remove(service, s.account) != nil
		}
	} else {
		// Remove an older token after a failed replacement; marker stays disabled.
		err = s.remove(service, s.account)
		status.RemovalPending = err != nil && !errors.Is(err, keyring.ErrNotFound)
	}
	if status.RemovalPending {
		_ = s.markerState("disabled-removal-pending")
	}
	s.mu.Lock()
	s.token, s.status = token, status
	s.mu.Unlock()
	return status, nil
}
func (s *Store) Disconnect() (Status, error) {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	if err := s.markerState("disabled"); err != nil {
		return s.Status(), errors.New("could not save disconnected state")
	}
	// Disable immediately, even when the operating system must wait for its
	// credential service. Generation never waits for a keychain dialog.
	s.mu.Lock()
	s.token, s.status = "", Status{}
	s.mu.Unlock()
	status := Status{}
	if err := s.remove(service, s.account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		status.RemovalPending = true
	}
	if status.RemovalPending {
		_ = s.markerState("disabled-removal-pending")
	}
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
	return status, nil
}
