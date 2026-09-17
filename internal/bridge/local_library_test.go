package bridge

import (
	"context"
	"errors"
	"testing"
)

func TestChooseLocalLibraryPathDistinguishesCancelAndError(t *testing.T) {
	path, canceled, err := chooseLocalLibraryPath(func() (string, error) { return "  /tmp/music.paipack  ", nil })
	if err != nil || canceled || path != "  /tmp/music.paipack  " {
		t.Fatalf("chosen = %q canceled=%v err=%v", path, canceled, err)
	}
	path, canceled, err = chooseLocalLibraryPath(func() (string, error) { return "", nil })
	if err != nil || !canceled || path != "" {
		t.Fatalf("cancel = %q canceled=%v err=%v", path, canceled, err)
	}
	want := errors.New("dialog failed")
	if _, _, err = chooseLocalLibraryPath(func() (string, error) { return "", want }); !errors.Is(err, want) {
		t.Fatalf("dialog error = %v", err)
	}
}

func TestLocalLibraryBridgeEmptyStatusAndValidation(t *testing.T) {
	api := New(newTestContainer(t), nil)
	status, err := api.GetLocalLibraryStatus()
	if err != nil || status.Installed || status.Mode != "combined" {
		t.Fatalf("empty status = %+v, %v", status, err)
	}
	if _, err := api.ImportLocalLibraryPack(context.Background(), "   "); err == nil {
		t.Fatal("empty import path accepted")
	}
	if _, err := api.SetLocalLibraryMode("library_only"); err == nil {
		t.Fatal("library-only mode accepted without a library")
	}
}

func TestCancelLocalLibraryImportCancelsOnlyItsOperation(t *testing.T) {
	api := New(newTestContainer(t), nil)
	ctx, _, finish := api.operations.begin(context.Background(), localLibraryImportOperation)
	defer finish()
	other, _, finishOther := api.operations.begin(context.Background(), "unrelated")
	defer finishOther()
	api.CancelLocalLibraryImport()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("local import was not canceled")
	}
	if other.Err() != nil {
		t.Fatalf("unrelated operation canceled: %v", other.Err())
	}
}
