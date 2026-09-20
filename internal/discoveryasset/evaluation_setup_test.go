package discoveryasset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/dataset"
	"github.com/platten/playlistai/internal/modelpack"
)

// TestPreparePersistentEvaluationAsset is explicitly opt-in. Unlike ordinary
// regression fixtures, its installation remains available for a separate live
// evaluation process. It never opens the normal application data directory.
func TestPreparePersistentEvaluationAsset(t *testing.T) {
	root := os.Getenv("PLAYLISTAI_EVAL_ASSET_ROOT")
	if root == "" {
		t.Skip("set PLAYLISTAI_EVAL_ASSET_ROOT to prepare an isolated persistent evaluation asset")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateEvaluationRoot(abs); err != nil {
		t.Fatal(err)
	}
	parts := os.Getenv("PLAYLISTAI_EVAL_PARTS")
	if parts == "" {
		t.Fatal("PLAYLISTAI_EVAL_PARTS is required")
	}
	ctx := context.Background()
	manifest, err := modelpack.ReadManifest(ctx, filepath.Join(parts, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = validateMultipart(manifest, 4<<20); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if status := manager.Status(); status.Installed {
		if status.Source != "hosted" || status.Tracks != 67913 {
			t.Fatalf("existing evaluation installation differs: %+v", status)
		}
		t.Logf("verified existing persistent evaluation asset: %d tracks at %s", status.Tracks, abs)
		return
	}
	cache := filepath.Join(abs, "transport-cache")
	if err = os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	var cachedBytes int64
	for _, part := range manifest.Parts {
		if validURL(part.Path) {
			t.Fatal("evaluation cache seed must use local relative parts")
		}
		source := filepath.Join(parts, filepath.FromSlash(part.Path))
		if err = dataset.VerifyFile(ctx, source, part.Size, part.SHA256); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(cache, strings.ToLower(part.SHA256)+".partdata")
		if err = dataset.VerifyFile(ctx, target, part.Size, part.SHA256); err != nil {
			if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
				t.Fatal("existing evaluation cache entry failed verification")
			}
			if err = copyFile(ctx, source, target); err != nil {
				t.Fatal(err)
			}
		}
		cachedBytes += part.Size
	}
	t.Logf("preseeded %d verified transport bytes from local archive parts", cachedBytes)
	started := time.Now()
	lastNote := ""
	progress := progressCallback(func(_ string, _, _ int64, note string) {
		if note != lastNote {
			lastNote = note
			t.Logf("asset setup phase: %s", note)
		}
	})
	status, err := manager.Install(ctx, "https://pub-233adf724b7e476db67cf787cd301c9e.r2.dev/paipack/manifest.json", progress)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.Source != "hosted" || status.Tracks != 67913 {
		t.Fatalf("unexpected persistent asset status: %+v", status)
	}
	catalogs, release, err := manager.Pin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if len(catalogs) != 1 {
		t.Fatalf("expected one hosted pack, got %d", len(catalogs))
	}
	t.Logf("persistent evaluation asset ready: %d tracks, %d packs, digest %s, root %s, elapsed %s", status.Tracks, len(catalogs), status.ManifestDigest, abs, time.Since(started))
}

func validateEvaluationRoot(abs string) error {
	if !filepath.IsAbs(abs) || filepath.Base(abs) != "discovery-data" || filepath.Base(filepath.Dir(abs)) != "data" || !strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(abs))), "enhanced-40-eval-") {
		return fmt.Errorf("evaluation asset root must be inside a dedicated enhanced-40-eval-*/data/discovery-data directory")
	}
	for current := abs; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("evaluation asset root must not traverse symbolic links")
			}
			if !info.IsDir() {
				return fmt.Errorf("evaluation asset root has a non-directory component")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func TestEvaluationRootRejectsSymlinkedComponents(t *testing.T) {
	root := filepath.Join(t.TempDir(), "enhanced-40-eval-isolation")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "data", "discovery-data")
	if err := validateEvaluationRoot(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "data")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := validateEvaluationRoot(target); err == nil {
		t.Fatal("symlinked evaluation component accepted")
	}
}
