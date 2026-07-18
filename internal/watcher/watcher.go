// Package watcher implements file watching and content-addressed sync.
package watcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yourorg/loopany-go/internal/protocol"
)

const (
	BlobCap        = 10 * 1024 * 1024 // 10MB
	InlineCap      = 64 * 1024        // 64KB
	InlineTotalCap = 1024 * 1024      // 1MB
	CoalesceMs     = 1500
	MaxSyncFiles   = 5000
	MaxSyncBytes   = 256 * 1024 * 1024 // 256MB
)

// IgnoreDirs are never synced.
var IgnoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".worktrees":   true,
	".loopany":     true,
	"__pycache__":  true,
	".cache":       true,
	".DS_Store":    true,
	"target":       true,
	"dist":         true,
	"build":        true,
}

// Watcher monitors a loop folder and syncs changes.
type Watcher struct {
	loopID    string
	path      string
	server    string
	token     string
	mu        sync.Mutex
	lastHash  map[string]string // path -> hash cache
	lastDigest string
	stopCh    chan struct{}
	flushCh   chan struct{}
}

// NewWatcher creates a folder watcher.
func NewWatcher(loopID, path, server, token string) (*Watcher, error) {
	// Verify path exists
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("path does not exist: %s", path)
	}

	return &Watcher{
		loopID:   loopID,
		path:     path,
		server:   server,
		token:    token,
		lastHash: make(map[string]string),
		stopCh:   make(chan struct{}),
		flushCh:  make(chan struct{}, 1),
	}, nil
}

// Start begins watching and syncing.
func (w *Watcher) Start(ctx context.Context) {
	// Initial flush
	w.flush(ctx)

	// Periodic sync
	ticker := time.NewTicker(CoalesceMs * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-w.flushCh:
			w.flush(ctx)
		case <-ticker.C:
			w.flush(ctx)
		}
	}
}

// Stop stops the watcher.
func (w *Watcher) Stop() {
	close(w.stopCh)
}

// FlushNow triggers immediate sync.
func (w *Watcher) FlushNow(ctx context.Context) {
	select {
	case w.flushCh <- struct{}{}:
	default:
	}
}

// flush builds manifest and syncs to server.
func (w *Watcher) flush(ctx context.Context) error {
	manifest, err := w.buildManifest()
	if err != nil {
		return err
	}

	// Skip if unchanged
	if manifest.Digest == w.lastDigest {
		return nil
	}

	// Post manifest to server
	needHashes, err := w.syncManifest(ctx, manifest)
	if err != nil {
		return err
	}

	// Upload missing blobs
	for _, hash := range needHashes {
		w.uploadBlob(ctx, manifest, hash)
	}

	w.lastDigest = manifest.Digest
	return nil
}

// buildManifest creates file manifest with hashes.
func (w *Watcher) buildManifest() (*protocol.WatchManifest, error) {
	manifest := &protocol.WatchManifest{
		LoopID: w.loopID,
		Files:  []protocol.WatchManifestEntry{},
	}

	var totalSize int64
	var hashBuilder strings.Builder

	err := filepath.Walk(w.path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip ignored directories
		if info.IsDir() {
			if IgnoreDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Size cap check
		if totalSize+info.Size() > MaxSyncBytes || len(manifest.Files) >= MaxSyncFiles {
			return nil
		}

		// Calculate hash
		hash, err := w.fileHash(path, info)
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(w.path, path)

		entry := protocol.WatchManifestEntry{
			Path:    relPath,
			Hash:    hash,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}

		// Inline small files
		if info.Size() <= InlineCap {
			data, err := os.ReadFile(path)
			if err == nil {
				entry.Bytes = data
			}
		}

		manifest.Files = append(manifest.Files, entry)
		totalSize += info.Size()
		hashBuilder.WriteString(hash)

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Calculate manifest digest
	h := sha256.Sum256([]byte(hashBuilder.String()))
	manifest.Digest = hex.EncodeToString(h[:])

	return manifest, nil
}

// fileHash returns file hash with caching.
func (w *Watcher) fileHash(path string, info os.FileInfo) (string, error) {
	w.mu.Lock()
	cacheKey := fmt.Sprintf("%s:%d:%d", path, info.Size(), info.ModTime().UnixNano())
	if cached, ok := w.lastHash[cacheKey]; ok {
		w.mu.Unlock()
		return cached, nil
	}
	w.mu.Unlock()

	// Calculate hash
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	io.CopyN(h, f, BlobCap) // Cap read for large files
	hash := hex.EncodeToString(h.Sum(nil))

	w.mu.Lock()
	w.lastHash[cacheKey] = hash
	w.mu.Unlock()

	return hash, nil
}

func (w *Watcher) syncManifest(ctx context.Context, manifest *protocol.WatchManifest) ([]string, error) {
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", w.server+"/api/machine/sync", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sync returned %d: %s", resp.StatusCode, string(respBody))
	}

	var needHashes []string
	if err := json.NewDecoder(resp.Body).Decode(&needHashes); err != nil {
		return nil, err
	}

	return needHashes, nil
}

func (w *Watcher) uploadBlob(ctx context.Context, manifest *protocol.WatchManifest, hash string) {
	for _, entry := range manifest.Files {
		if entry.Hash == hash && entry.Bytes != nil {
			req, err := http.NewRequestWithContext(ctx, "PUT", w.server+"/api/machine/blob/"+hash, bytes.NewReader(entry.Bytes))
			if err != nil {
				return
			}

			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Authorization", "Bearer "+w.token)

			client := &http.Client{Timeout: 120 * time.Second}
			client.Do(req)
			return
		}
	}
}