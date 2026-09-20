// Command indexerpack creates a reproducible self-extracting playlist-indexer.
package main

import (
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/indexerbundle"
	"github.com/platten/playlistai/internal/localaudio"
)

type input struct{ prefix, root string }

func main() {
	launcher := flag.String("launcher", "", "unpacked playlist-indexer executable")
	codec := flag.String("codec", "", "verified codec payload directory")
	model := flag.String("model", "", "optional verified CPU MERT bundle directory")
	cudaModel := flag.String("cuda-model", "", "optional verified CUDA MERT bundle directory; requires --model")
	clapModel := flag.String("clap-model", "", "optional verified CPU CLAP bundle directory")
	clapCUDAModel := flag.String("clap-cuda-model", "", "optional verified CUDA CLAP bundle directory; requires --clap-model")
	out := flag.String("out", "", "output executable")
	validateOffline := flag.Bool("validate-offline", false, "validate complete CPU/CUDA MERT and CLAP inputs without packaging")
	validatePackage := flag.String("validate-package", "", "validate an already packaged executable")
	requireOffline := flag.Bool("require-offline", false, "require CPU/CUDA MERT and CLAP payloads with --validate-package")
	flag.Parse()
	if *validatePackage != "" {
		if err := validatePackagedExecutable(*validatePackage, *requireOffline); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "validated packaged executable:", *validatePackage)
		return
	}
	if *validateOffline {
		if err := validateOfflineModels(*model, *cudaModel, *clapModel, *clapCUDAModel); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "validated offline CPU/CUDA MERT and CLAP bundles")
		return
	}
	if err := pack(*launcher, *codec, *model, *cudaModel, *clapModel, *clapCUDAModel, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validatePackagedExecutable(path string, requireOffline bool) error {
	bundle, err := indexerbundle.OpenExecutable(path)
	if err != nil {
		return fmt.Errorf("indexerpack: open packaged executable: %w", err)
	}
	defer bundle.Close()

	required := []string{"codec"}
	if requireOffline {
		required = append(required, "mert/cpu", "mert/cuda", "clap/cpu", "clap/cuda")
	}
	for _, prefix := range required {
		if !bundle.Has(prefix) {
			return fmt.Errorf("indexerpack: packaged executable is missing %s payload", prefix)
		}
	}

	codecDir, err := os.MkdirTemp("", "playlist-indexer-codec-validation-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(codecDir)
	if err := bundle.Extract("codec", codecDir); err != nil {
		return fmt.Errorf("indexerpack: extract packaged codec payload: %w", err)
	}
	if _, err := localaudio.OpenRuntime(codecDir); err != nil {
		return fmt.Errorf("indexerpack: invalid packaged codec payload: %w", err)
	}
	return nil
}

func validateOfflineModels(model, cudaModel, clapModel, clapCUDAModel string) error {
	bundles := []struct{ name, path string }{{"CPU MERT", model}, {"CUDA MERT", cudaModel}, {"CPU CLAP", clapModel}, {"CUDA CLAP", clapCUDAModel}}
	for _, bundle := range bundles {
		if strings.TrimSpace(bundle.path) == "" {
			return fmt.Errorf("indexerpack: offline build requires %s bundle", bundle.name)
		}
	}
	for _, bundle := range bundles {
		name, path := bundle.name, bundle.path
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("indexerpack: %s bundle is unavailable: %w", name, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("indexerpack: %s bundle is not a directory", name)
		}
	}
	cpuMERT, err := audio.ReadMERTBundle(model)
	if err != nil {
		return fmt.Errorf("indexerpack: invalid CPU MERT payload: %w", err)
	}
	cudaMERT, err := audio.ReadMERTBundle(cudaModel)
	if err != nil {
		return fmt.Errorf("indexerpack: invalid CUDA MERT payload: %w", err)
	}
	if cpuMERT.Backend() != "cpu" || cudaMERT.Backend() != "cuda" || cpuMERT.Model.WeightsSHA256 != cudaMERT.Model.WeightsSHA256 || cpuMERT.Model.Model != cudaMERT.Model.Model || cpuMERT.Model.Revision != cudaMERT.Model.Revision || cpuMERT.Model.Dimension != cudaMERT.Model.Dimension {
		return errors.New("indexerpack: CPU and CUDA MERT bundles do not describe the same embedding model")
	}
	cpuCLAP, err := audio.ReadBundle(clapModel)
	if err != nil {
		return fmt.Errorf("indexerpack: invalid CPU CLAP payload: %w", err)
	}
	cudaCLAP, err := audio.ReadBundle(clapCUDAModel)
	if err != nil {
		return fmt.Errorf("indexerpack: invalid CUDA CLAP payload: %w", err)
	}
	if cpuCLAP.Backend() != "cpu" || cudaCLAP.Backend() != "cuda" || cpuCLAP.EmbeddingFingerprint() != cudaCLAP.EmbeddingFingerprint() {
		return errors.New("indexerpack: CPU and CUDA CLAP bundles do not describe the same embedding model")
	}
	return nil
}

func pack(launcher, codec, model, cudaModel, clapModel, clapCUDAModel, out string) error {
	if launcher == "" || codec == "" || out == "" {
		return errors.New("indexerpack: --launcher, --codec, and --out are required")
	}
	codecAbs, err := filepath.Abs(codec)
	if err != nil {
		return err
	}
	if _, err := localaudio.OpenRuntime(codecAbs); err != nil {
		return fmt.Errorf("indexerpack: invalid codec payload: %w", err)
	}
	inputs := []input{{"codec", codecAbs}}
	if cudaModel != "" && model == "" {
		return errors.New("indexerpack: --cuda-model requires the CPU --model bundle")
	}
	if model != "" {
		modelAbs, err := filepath.Abs(model)
		if err != nil {
			return err
		}
		manifest, err := audio.ReadMERTBundle(modelAbs)
		if err != nil {
			return fmt.Errorf("indexerpack: invalid MERT payload: %w", err)
		}
		if cudaModel == "" {
			inputs = append(inputs, input{"mert", modelAbs})
		} else {
			if manifest.Backend() != "cpu" {
				return errors.New("indexerpack: --model must be a CPU MERT bundle when --cuda-model is present")
			}
			cudaAbs, err := filepath.Abs(cudaModel)
			if err != nil {
				return err
			}
			cudaManifest, err := audio.ReadMERTBundle(cudaAbs)
			if err != nil {
				return fmt.Errorf("indexerpack: invalid CUDA MERT payload: %w", err)
			}
			if cudaManifest.Backend() != "cuda" {
				return errors.New("indexerpack: --cuda-model must declare the CUDA backend")
			}
			inputs = append(inputs, input{"mert/cpu", modelAbs}, input{"mert/cuda", cudaAbs})
		}
	}
	if clapCUDAModel != "" && clapModel == "" {
		return errors.New("indexerpack: --clap-cuda-model requires the CPU --clap-model bundle")
	}
	if clapModel != "" {
		cpuAbs, err := filepath.Abs(clapModel)
		if err != nil {
			return err
		}
		manifest, err := audio.ReadBundle(cpuAbs)
		if err != nil {
			return fmt.Errorf("indexerpack: invalid CPU CLAP payload: %w", err)
		}
		if manifest.Backend() != "cpu" {
			return errors.New("indexerpack: --clap-model must declare the CPU backend")
		}
		if clapCUDAModel == "" {
			inputs = append(inputs, input{"clap", cpuAbs})
		} else {
			cudaAbs, err := filepath.Abs(clapCUDAModel)
			if err != nil {
				return err
			}
			cudaManifest, err := audio.ReadBundle(cudaAbs)
			if err != nil {
				return fmt.Errorf("indexerpack: invalid CUDA CLAP payload: %w", err)
			}
			if cudaManifest.Backend() != "cuda" {
				return errors.New("indexerpack: --clap-cuda-model must declare the CUDA backend")
			}
			if cudaManifest.EmbeddingFingerprint() != manifest.EmbeddingFingerprint() {
				return errors.New("indexerpack: CPU and CUDA CLAP bundles have different embedding identities")
			}
			inputs = append(inputs, input{"clap/cpu", cpuAbs}, input{"clap/cuda", cudaAbs})
		}
	}
	launcherFile, err := os.Open(launcher)
	if err != nil {
		return err
	}
	defer launcherFile.Close()
	info, err := launcherFile.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("indexerpack: launcher is not regular")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".playlist-indexer-pack-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, launcherFile); err != nil {
		tmp.Close()
		return err
	}
	start, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		tmp.Close()
		return err
	}
	zw := zip.NewWriter(tmp)
	for _, source := range inputs {
		if err := addTree(zw, source); err != nil {
			zw.Close()
			tmp.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return err
	}
	end, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(indexerbundle.Trailer(uint64(end - start))); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, out); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(out))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func addTree(zw *zip.Writer, source input) error {
	var paths []string
	err := filepath.WalkDir(source.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source.root {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("indexerpack: payload symlinks are forbidden")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("indexerpack: payload files must be regular")
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, err := filepath.Rel(source.root, path)
		if err != nil {
			return err
		}
		name := source.prefix + "/" + filepath.ToSlash(rel)
		if !fs.ValidPath(name) || strings.Contains(name, "\\") {
			return errors.New("indexerpack: invalid payload path")
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name, header.Method = name, zip.Store
		header.Modified = time.Unix(0, 0).UTC()
		header.SetMode(info.Mode().Perm())
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}
