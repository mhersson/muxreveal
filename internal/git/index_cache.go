package git

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// indexCache keeps the refreshed temporary index of one worktree in the cache
// dir, named after the stat of the user's index it was copied from. It is
// best effort: every failure leaves the caller on the user's index.
type indexCache struct {
	dir    string
	prefix string
	name   string // empty when the user's index cannot be stat'ed
}

// openIndexCache returns nil when root is empty.
func openIndexCache(root, gitDir, realIndex string) *indexCache {
	if root == "" {
		return nil
	}

	sum := sha256.Sum256([]byte(gitDir))
	c := &indexCache{dir: filepath.Join(root, "index"), prefix: hex.EncodeToString(sum[:])[:16]}

	if info, err := os.Stat(realIndex); err == nil && info.Mode().IsRegular() {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			c.name = fmt.Sprintf("%s-%d-%d-%d.idx", c.prefix, info.Size(), info.ModTime().UnixNano(), st.Ino)
		}
	}

	return c
}

// path is the cache file for the user's index as it was stat'ed, or "".
func (c *indexCache) path() string {
	if c == nil || c.name == "" {
		return ""
	}

	return filepath.Join(c.dir, c.name)
}

// store replaces the cache file with a copy of src and removes the worktree's
// older files. The copy keeps src's mtime and lands by rename, so a reader sees
// the old file or the new one, and git's racy-entry checks stay valid.
func (c *indexCache) store(src string) {
	dst := c.path()
	if dst == "" {
		return
	}

	info, err := os.Stat(src)
	if err != nil || info.Size() == 0 {
		return
	}

	data, err := os.ReadFile(src)
	if err != nil || len(data) == 0 {
		return
	}

	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return
	}

	f, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return
	}

	_, werr := f.Write(data)
	cerr := f.Close()

	if werr != nil || cerr != nil || os.Chtimes(f.Name(), info.ModTime(), info.ModTime()) != nil ||
		os.Rename(f.Name(), dst) != nil {
		_ = os.Remove(f.Name())

		return
	}

	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if n := e.Name(); n != c.name && strings.HasPrefix(n, c.prefix+"-") && strings.HasSuffix(n, ".idx") {
			_ = os.Remove(filepath.Join(c.dir, n))
		}
	}
}
