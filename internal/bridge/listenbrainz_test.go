package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestListenBrainzBridgeRejectsTokensWithoutReturningOrSavingThem(t *testing.T) {
	api := New(newTestContainer(t), nil)
	secret := "synthetic-private-invalid-token"
	status, err := api.ConnectListenBrainz(context.Background(), secret)
	if err == nil || status.Connected || strings.Contains(err.Error(), secret) {
		t.Fatal("unsafe validation result", status, err)
	}
	raw, err := json.Marshal(api.GetListenBrainzStatus())
	if err != nil || strings.Contains(strings.ToLower(string(raw)), "token") || strings.Contains(string(raw), secret) {
		t.Fatal("credential appeared in status", string(raw), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	status, err = api.ConnectListenBrainz(ctx, "11111111-1111-4111-8111-111111111111")
	if !errors.Is(err, context.Canceled) || status.Connected {
		t.Fatal("canceled connection committed", status, err)
	}
}
