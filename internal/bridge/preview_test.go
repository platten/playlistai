package bridge

import "testing"

func TestGetPreviewURLNoProvider(t *testing.T) {
	t.Parallel()
	// config.Default() has preview.provider = "deezer", but the bare container
	// (no catalog) should still return a clean miss, not an error.
	api := New(newTestContainer(t), nil)
	res, err := api.GetPreviewURL("seed0001")
	if err != nil {
		t.Fatalf("GetPreviewURL: %v", err)
	}
	if res.Available || res.URL != "" {
		t.Fatalf("no catalog => must be unavailable, got %+v", res)
	}
}

func TestGetPreviewURLUnknownID(t *testing.T) {
	t.Parallel()
	api := New(newLoadedContainer(t), nil)
	res, err := api.GetPreviewURL("not-a-real-id")
	if err != nil {
		t.Fatalf("GetPreviewURL: %v", err)
	}
	if res.Available {
		t.Fatalf("unknown id should be a clean miss, got %+v", res)
	}
}

func TestSetAndGetPreviewProviderName(t *testing.T) {
	t.Parallel()
	api := New(newTestContainer(t), nil)

	if got := api.GetPreviewProviderName(); got != "deezer" {
		t.Fatalf("default provider = %q, want deezer", got)
	}
	if err := api.SetPreviewProvider("off"); err != nil {
		t.Fatalf("SetPreviewProvider: %v", err)
	}
	if got := api.GetPreviewProviderName(); got != "off" {
		t.Fatalf("provider = %q, want off", got)
	}
	if err := api.SetPreviewProvider("bogus"); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}
