package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// resolveCache keeps one built *bonsai.Resolved warm per (config, source fingerprint). The build
// is the expensive step; what-if queries are cheap. Caching by a source fingerprint makes the
// agent's edit loop "just work": repeated tool calls reuse the build, and the moment the agent
// edits the source the fingerprint changes and the next call rebuilds — no handles for the agent
// to manage, no stale results.
//
// Tool calls are fully serialized via with: the SDK dispatches tool handlers concurrently, so a
// query against a *Resolved must not overlap a concurrent rebuild that would Close it out from
// under the reader (nor a second query racing the same buildGraph). Holding mu for the whole
// handler body guarantees one build, one query, at a time — which matches the local single-agent
// model this server targets.
type resolveCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	resolved    *bonsai.Resolved
	fingerprint string
}

func newResolveCache() *resolveCache {
	return &resolveCache{entries: map[string]*cacheEntry{}}
}

// with builds (or reuses) the resolved target for cfg and invokes fn with it while holding the
// cache lock. Holding mu across fn is what makes the *Resolved safe to read: it cannot be Closed
// by a concurrent rebuild, and no second handler can touch the same buildGraph in parallel. The
// *Resolved is owned by the cache and valid only for the duration of fn — callers must not retain
// or Close it.
func (c *resolveCache) with(cfg bonsai.Config, fn func(*bonsai.Resolved) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	resolved, err := c.resolveLocked(cfg)
	if err != nil {
		return err
	}
	return fn(resolved)
}

// resolveLocked returns a resolved target for cfg, rebuilding if the source has changed since the
// last call (or this is the first call). The caller must hold c.mu.
func (c *resolveCache) resolveLocked(cfg bonsai.Config) (*bonsai.Resolved, error) {
	key := configKey(cfg)
	fp := sourceFingerprint(cfg)
	if e := c.entries[key]; e != nil {
		if e.fingerprint == fp {
			return e.resolved, nil
		}
		e.resolved.Close() // source changed: drop the stale build before rebuilding
		delete(c.entries, key)
	}

	resolved, err := bonsai.Resolve(cfg)
	if err != nil {
		return nil, err
	}
	c.entries[key] = &cacheEntry{resolved: resolved, fingerprint: fp}
	return resolved, nil
}

// close releases every cached build artifact. Call on server shutdown.
func (c *resolveCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		e.resolved.Close()
	}
	c.entries = map[string]*cacheEntry{}
}

// configKey identifies a distinct analysis target: same key => same build, modulo source edits
// (handled by the fingerprint).
func configKey(cfg bonsai.Config) string {
	return strings.Join([]string{
		cfg.Dir, cfg.Target, cfg.Binary,
		strings.Join(cfg.Controlled, ","),
		strings.Join(cfg.Locked, ","),
		strings.Join(cfg.Unlock, ","),
	}, "\x00")
}

// sourceFingerprint hashes the contents of every Go source and module file under the target
// directory (or the prebuilt binary, in binary mode) so a single edit invalidates the cached
// build. It hashes contents rather than size+mtime so the agent's edit loop is not at the mercy
// of coarse filesystem mtime resolution (a same-length rewrite within one mtime tick would
// otherwise read as unchanged). It is still far cheaper than the build it guards.
func sourceFingerprint(cfg bonsai.Config) string {
	h := sha256.New()
	if cfg.Binary != "" {
		stampFile(h, cfg.Binary)
		// a prebuilt binary is the whole input; don't tie the fingerprint to an unrelated
		// working directory when no source dir was given.
		if cfg.Dir == "" {
			return hex.EncodeToString(h.Sum(nil))
		}
	}
	dir := cfg.Dir
	if dir == "" {
		dir = "."
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate unreadable trees; the build will surface real errors
		}
		if d.IsDir() {
			base := d.Name()
			if base == "vendor" || base == "testdata" || base == ".git" || (strings.HasPrefix(base, ".") && base != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".mod") || strings.HasSuffix(path, ".sum") {
			stampFile(h, path)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// stampFile folds a file's path and contents into h. Content hashing (not size+mtime) is what
// lets a same-length, same-tick rewrite still change the fingerprint.
func stampFile(h hash.Hash, path string) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(h, "%s|unreadable\n", path)
		return
	}
	defer f.Close()
	fmt.Fprintf(h, "%s|", path)
	if _, err := io.Copy(h, f); err != nil {
		fmt.Fprintf(h, "|error:%v", err)
	}
	fmt.Fprintln(h)
}
