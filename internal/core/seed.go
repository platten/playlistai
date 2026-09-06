package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// RNGSeed is the lossless decimal representation of all 64 seed bits. Its text
// marshaler makes JSON and generated TypeScript carry it as a string so
// JavaScript cannot round it through Number. UnmarshalJSON also accepts
// historical integer values from saved requests.
type RNGSeed string

const ZeroRNGSeed RNGSeed = "0"

func NewRNGSeed(bits uint64) RNGSeed { return RNGSeed(strconv.FormatUint(bits, 10)) }

func (s RNGSeed) IsZero() bool { return s == "" || s == ZeroRNGSeed }

func (s RNGSeed) Canonical() (RNGSeed, error) {
	raw := strings.TrimSpace(string(s))
	if raw == "" {
		return ZeroRNGSeed, nil
	}
	if value, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return NewRNGSeed(value), nil
	}
	// Historical seeds were signed int64 JSON numbers. Preserve their bit
	// pattern while moving to the unsigned decimal representation.
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return NewRNGSeed(uint64(value)), nil
	}
	return "", fmt.Errorf("invalid RNG seed %q", raw)
}

func (s RNGSeed) Int64() (int64, error) {
	canonical, err := s.Canonical()
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(string(canonical), 10, 64)
	return int64(value), err
}

func (s RNGSeed) MarshalText() ([]byte, error) {
	canonical, err := s.Canonical()
	if err != nil {
		return nil, err
	}
	return []byte(canonical), nil
}

func (s *RNGSeed) UnmarshalJSON(data []byte) error {
	if s == nil {
		return fmt.Errorf("nil RNGSeed receiver")
	}
	raw := strings.TrimSpace(string(data))
	if bytes.Equal(data, []byte("null")) || raw == "null" || raw == "" {
		*s = ZeroRNGSeed
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		raw = value
	}
	canonical, err := RNGSeed(raw).Canonical()
	if err != nil {
		return err
	}
	*s = canonical
	return nil
}
