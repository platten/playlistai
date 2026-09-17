package libraryindex

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type sampleCandidate struct {
	path     string
	priority [32]byte
}

type worstSampleHeap []sampleCandidate

func (h worstSampleHeap) Len() int      { return len(h) }
func (h worstSampleHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h worstSampleHeap) Less(i, j int) bool {
	return strings.Compare(string(h[i].priority[:]), string(h[j].priority[:])) > 0
}
func (h *worstSampleHeap) Push(value any) { *h = append(*h, value.(sampleCandidate)) }
func (h *worstSampleHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

// SampleAudioFiles selects a deterministic bounded sample without retaining
// one descriptor per source file. It is used only by the opt-in benchmark.
func SampleAudioFiles(ctx context.Context, root string, limit int, seed uint64) ([]string, error) {
	if limit <= 0 {
		return nil, errors.New("library indexer: sample size must be positive")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	selected := &worstSampleHeap{}
	heap.Init(selected)
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		if _, ok := audioExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		h := sha256.New()
		var raw [8]byte
		binary.LittleEndian.PutUint64(raw[:], seed)
		h.Write([]byte("playlist-indexer-bench-sample/v1"))
		h.Write(raw[:])
		h.Write([]byte(filepath.ToSlash(rel)))
		var priority [32]byte
		copy(priority[:], h.Sum(nil))
		candidate := sampleCandidate{path: path, priority: priority}
		if selected.Len() < limit {
			heap.Push(selected, candidate)
		} else if strings.Compare(string(priority[:]), string((*selected)[0].priority[:])) < 0 {
			heap.Pop(selected)
			heap.Push(selected, candidate)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, len(*selected))
	for i, item := range *selected {
		out[i] = item.path
	}
	sort.Strings(out)
	return out, nil
}

func (s *State) ObserveSamplePath(ctx context.Context, epoch int64, root Root, path string, semanticJobs map[string]string) error {
	rel, err := filepath.Rel(root.Path, path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("library indexer: benchmark sample is not a regular file")
	}
	device, inode := fileIdentity(info)
	_, err = s.ObserveFile(ctx, epoch, SourceFile{RootID: root.ID, RelativePath: rel, Device: device, Inode: inode, Size: info.Size(), MTimeNS: info.ModTime().UnixNano(), Extension: strings.ToLower(filepath.Ext(path))}, semanticJobs)
	return err
}
