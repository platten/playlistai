package app

import (
	"context"
	"strings"

	"github.com/platten/playlistai/internal/credentials"
	"github.com/platten/playlistai/internal/musicgraph"
)

func (c *Container) listenBrainzStore() *credentials.Store {
	c.listenBrainzMu.Lock()
	defer c.listenBrainzMu.Unlock()
	if c.listenBrainz == nil {
		c.listenBrainz = credentials.New(c.cfg.DataDir)
	}
	return c.listenBrainz
}

// ReadListenBrainzStatus waits for a pending credential commit so a canceled
// UI operation can refresh authoritative state. Generation reads the immediate
// snapshot through ListenBrainzStatus and never waits for connection validation.
func (c *Container) ReadListenBrainzStatus() credentials.Status {
	c.listenBrainzChangeMu.Lock()
	defer c.listenBrainzChangeMu.Unlock()
	return c.listenBrainzStore().Status()
}
func (c *Container) ListenBrainzStatus() credentials.Status { return c.listenBrainzStore().Status() }
func (c *Container) ListenBrainzClient() (*musicgraph.Client, error) {
	store := c.listenBrainzStore()
	c.listenBrainzMu.Lock()
	defer c.listenBrainzMu.Unlock()
	if c.listenBrainzClient == nil {
		client, err := musicgraph.NewAuthenticatedClient(store.Token)
		if err != nil {
			return nil, err
		}
		c.listenBrainzClient = client
	}
	return c.listenBrainzClient, nil
}
func (c *Container) ConnectListenBrainz(ctx context.Context, token string) (credentials.Status, error) {
	c.listenBrainzChangeMu.Lock()
	defer c.listenBrainzChangeMu.Unlock()
	client, err := c.ListenBrainzClient()
	if err != nil {
		return credentials.Status{}, err
	}
	if err = client.ValidateToken(ctx, token); err != nil {
		return c.listenBrainzStore().Status(), err
	}
	if err = ctx.Err(); err != nil {
		return c.listenBrainzStore().Status(), err
	}
	return c.listenBrainzStore().Connect(strings.TrimSpace(token))
}
func (c *Container) DisconnectListenBrainz() (credentials.Status, error) {
	c.listenBrainzChangeMu.Lock()
	defer c.listenBrainzChangeMu.Unlock()
	return c.listenBrainzStore().Disconnect()
}
