// Package store is the skill store: content-addressed, immutable, license-gated.
//
// It is deliberately **not** part of core/. The kernel must not know the word
// "skill" at all — that is Gate 4, enforced by tools/check_kernel.py. Everything
// here is an organ: it can be replaced without the kernel noticing, and the
// kernel would not have a word for what changed.
//
// The one thing this package borrows from core is the refusal vocabulary.
// That is deliberate. A second vocabulary would drift from the first, and the
// drift would be silent — this project already paid for that lesson once.
// tools/check_contract.py exists because the doc and the kernel disagreed about
// a refusal code name and neither side reported it.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"sansheng/core"
)

// CAS is a content-addressed blob store.
//
// The only way to name something here is by the hash of its bytes. There is no
// "latest", no "current", no mutable slot. Those live in Refs — pointers *into*
// this store, never part of it. Keeping the two apart is what makes rollback a
// pointer switch instead of a copy.
type CAS struct{ root string }

// OpenCAS opens (creating if needed) a content-addressed store rooted at dir.
func OpenCAS(dir string) (*CAS, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &CAS{root: dir}, nil
}

// Hash returns the hex sha256 of data. This is the only name a blob has.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ValidHash reports whether h is a well-formed content hash.
//
// This is a security boundary, not a formality. Hashes arrive from the wire,
// and a store that joins a client-supplied string into a path is a store that
// writes wherever the client says. Lowercase hex, exactly 64 characters.
func ValidHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (c *CAS) path(hash string) string {
	// Two-level fanout. A flat directory with 100k entries is slow on every
	// filesystem we care about, and this costs one slice.
	return filepath.Join(c.root, hash[:2], hash)
}

// Put stores data and returns its hash.
//
// Atomic by construction: the bytes go to a temp file in the same directory and
// only become visible under the hash via rename(2). A partial write can
// therefore never be observed *as* a content hash.
func (c *CAS) Put(data []byte) (string, error) {
	h := Hash(data)
	p := c.path(h)
	if _, err := os.Stat(p); err == nil {
		// Same hash means same bytes. There is nothing to write.
		return h, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename has happened
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// Immutable on disk, not merely immutable in intent.
	if err := os.Chmod(name, 0o444); err != nil {
		return "", err
	}
	if err := os.Rename(name, p); err != nil {
		return "", err
	}
	return h, nil
}

// Get returns the bytes stored under hash.
func (c *CAS) Get(hash string) ([]byte, error) {
	if !ValidHash(hash) {
		return nil, &core.Refusal{Code: core.CodeBadRequest, Reason: "not a content hash: " + hash}
	}
	data, err := os.ReadFile(c.path(hash))
	if err != nil {
		return nil, err
	}
	// Verify on read. A store that hands back bytes it cannot vouch for is not
	// content-addressed — it is merely content-labelled.
	if got := Hash(data); got != hash {
		return nil, &core.Refusal{
			Code:   core.CodeRecoveryRequired,
			Reason: hash + " holds bytes that hash to " + got,
		}
	}
	return data, nil
}

// Has reports presence only. It does not verify the bytes.
func (c *CAS) Has(hash string) bool {
	if !ValidHash(hash) {
		return false
	}
	_, err := os.Stat(c.path(hash))
	return err == nil
}

// PutJSON stores v as canonical JSON and returns (hash, canonical bytes).
//
// Canonical, not merely marshalled: the same value must produce the same bytes
// on every machine and every Go version, or the hash is not an identity.
func (c *CAS) PutJSON(v any) (string, []byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	canon, ref := core.Canonical(raw)
	if ref != nil {
		return "", nil, ref
	}
	h, err := c.Put(canon)
	return h, canon, err
}

// GetJSON reads a blob and decodes it into v.
func (c *CAS) GetJSON(hash string, v any) error {
	data, err := c.Get(hash)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
