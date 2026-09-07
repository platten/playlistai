package audio

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeArchiveExtractsOnlyVerifiedLibrary(t *testing.T) {
	for _, extension := range []string{".tgz", ".zip"} {
		t.Run(extension, func(t *testing.T) {
			dir := t.TempDir()
			data := []byte("native-library-fixture")
			hash := sha256.Sum256(data)
			artifact := BundleArtifact{Name: "runtime" + extension, ArchiveMember: "package/lib/runtime.dll", UnpackedSize: int64(len(data)), UnpackedSHA256: hex.EncodeToString(hash[:])}
			file, err := os.Create(filepath.Join(dir, artifact.Name))
			if err != nil {
				t.Fatal(err)
			}
			if extension == ".zip" {
				writer := zip.NewWriter(file)
				for _, name := range []string{"../../escape", artifact.ArchiveMember} {
					member, err := writer.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := member.Write(data); err != nil {
						t.Fatal(err)
					}
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				gz := gzip.NewWriter(file)
				writer := tar.NewWriter(gz)
				for _, name := range []string{"../../escape", artifact.ArchiveMember} {
					if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
						t.Fatal(err)
					}
					if _, err := writer.Write(data); err != nil {
						t.Fatal(err)
					}
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				if err := gz.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			artifact.UnpackedSHA256 = strings.Repeat("0", 64)
			if err := unpackRuntime(context.Background(), dir, artifact); err == nil {
				t.Fatal("corrupt runtime accepted")
			}
			if _, err := os.Stat(filepath.Join(dir, "runtime.dll")); !os.IsNotExist(err) {
				t.Fatal("failed runtime persisted")
			}
			artifact.UnpackedSHA256 = hex.EncodeToString(hash[:])
			if err := unpackRuntime(context.Background(), dir, artifact); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dir, "runtime.dll"))
			if err != nil || string(got) != string(data) {
				t.Fatal("wrong runtime contents")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 2 {
				t.Fatalf("unexpected extracted files: %v %v", entries, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := unpackRuntime(ctx, dir, artifact); err == nil {
				t.Fatal("canceled extraction succeeded")
			}
		})
	}
}

func TestRuntimeMemberPaths(t *testing.T) {
	for _, name := range []string{"../library", "/library", "C:/library", "a/../library", "a\\library"} {
		if safeArchiveMember(name) {
			t.Fatalf("unsafe member accepted: %s", name)
		}
	}
	if !safeArchiveMember("./package/lib/runtime.dylib") {
		t.Fatal("official macOS archive path rejected")
	}
}
