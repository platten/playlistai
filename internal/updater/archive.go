package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxUnpacked = 1 << 30

func unpack(ctx context.Context, archive, dir string, i installation) (string, error) {
	if i.Kind == "windows-installer" {
		return archive, nil
	}
	if i.Kind == "darwin" && strings.HasSuffix(archive, ".dmg") {
		return unpackDMG(ctx, archive, dir)
	}
	if i.Kind == "appimage" {
		to := filepath.Join(dir, "payload")
		if err := os.Rename(archive, to); err != nil {
			return "", err
		}
		return to, os.Chmod(to, 0755)
	}
	root := filepath.Join(dir, "unpacked")
	if err := os.Mkdir(root, 0700); err != nil {
		return "", err
	}
	var total int64
	count := 0
	write := func(name string, size int64, mode os.FileMode, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		total += size
		if size < 0 || total > maxUnpacked || count > 4096 {
			return fmt.Errorf("update archive exceeds extraction limits")
		}
		if strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != strings.TrimSuffix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("unsafe update archive path")
		}
		if mode&(os.ModeSymlink|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
			return fmt.Errorf("update archive contains unsupported links or special files")
		}
		if i.Kind == "darwin" {
			if strings.HasPrefix(name, "__MACOSX/") {
				return nil
			}
			if name != "playlist-ai.app/" && !strings.HasPrefix(name, "playlist-ai.app/") {
				return fmt.Errorf("unexpected application bundle entry")
			}
		} else if name != "playlist-ai" && name != "playlist-ai.exe" {
			return fmt.Errorf("unexpected executable archive entry")
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		cleanRoot := filepath.Clean(root)
		cleanTarget := filepath.Clean(target)
		rel, err := filepath.Rel(cleanRoot, cleanTarget)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe update archive path")
		}
		if mode.IsDir() {
			return os.MkdirAll(cleanTarget, 0755)
		}
		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()&0755|0600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(f, r, size)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if strings.HasSuffix(archive, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return "", err
		}
		defer z.Close()
		for _, entry := range z.File {
			if entry.UncompressedSize64 > maxUnpacked {
				return "", fmt.Errorf("update archive entry is too large")
			}
			r, err := entry.Open()
			if err != nil {
				return "", err
			}
			err = write(entry.Name, int64(entry.UncompressedSize64), entry.Mode(), r)
			_ = r.Close()
			if err != nil {
				return "", err
			}
		}
	} else {
		f, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if h.Typeflag != tar.TypeReg {
				return "", fmt.Errorf("unexpected tar entry type")
			}
			if err := write(h.Name, h.Size, h.FileInfo().Mode(), tr); err != nil {
				return "", err
			}
		}
	}
	name := "playlist-ai"
	if i.Kind == "windows" {
		name += ".exe"
	}
	if i.Kind == "darwin" {
		name += ".app"
	}
	payload := filepath.Join(root, name)
	if i.Kind != "darwin" {
		if err := os.Chmod(payload, 0755); err != nil {
			return "", err
		}
	}
	return payload, nil
}

func payloadExecutable(payload string, kind string) string {
	if kind == "darwin" {
		return filepath.Join(payload, "Contents", "MacOS", "playlist-ai")
	}
	return payload
}

func verifyArchitecture(executable, kind, arch string) error {
	valid := false
	switch kind {
	case "linux", "appimage":
		f, err := elf.Open(executable)
		if err != nil {
			return err
		}
		defer f.Close()
		valid = arch == "amd64" && f.Machine == elf.EM_X86_64 || arch == "arm64" && f.Machine == elf.EM_AARCH64
	case "windows", "windows-installer":
		f, err := pe.Open(executable)
		if err != nil {
			return err
		}
		defer f.Close()
		valid = arch == "amd64" && f.Machine == pe.IMAGE_FILE_MACHINE_AMD64 || arch == "arm64" && f.Machine == pe.IMAGE_FILE_MACHINE_ARM64
		if kind == "windows-installer" {
			valid = f.Machine == pe.IMAGE_FILE_MACHINE_I386 || f.Machine == pe.IMAGE_FILE_MACHINE_AMD64
		}
	case "darwin":
		matches := func(cpu macho.Cpu) bool {
			return arch == "arm64" && cpu == macho.CpuArm64 || arch == "amd64" && cpu == macho.CpuAmd64
		}
		if fat, err := macho.OpenFat(executable); err == nil {
			defer fat.Close()
			for _, a := range fat.Arches {
				valid = valid || matches(a.Cpu)
			}
		} else {
			f, err := macho.Open(executable)
			if err != nil {
				return err
			}
			defer f.Close()
			valid = matches(f.Cpu)
		}
	}
	if !valid {
		return fmt.Errorf("the downloaded application does not support this computer's %s architecture", arch)
	}
	return nil
}

// Hash the complete staged bundle, including paths and modes. Symlinks are never
// followed: neither a downloaded link nor an unexpected installed link is safe
// to replace through this portable updater.
func treeHash(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("application contains a link or special file")
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\x00", rel, info.Mode(), info.Size())
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(h, f)
		_ = f.Close()
		return err
	})
	return hex.EncodeToString(h.Sum(nil)), err
}
