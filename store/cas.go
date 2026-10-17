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

	"teps/core"
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

// ★ A content hash is a **triple**, not a string.
//
// The three parts are: which algorithm, which version of it, and which
// normalization the bytes went through before hashing. The string that gets
// stored is only the third part; the first two used to be implicit, and an
// implicit part of an identity is the part that goes wrong silently.
//
// What that cost, concretely: `Get` recomputes with sha256 and compares. If a
// digest produced by some other 64-hex algorithm arrives, `ValidHash` accepts it
// on shape, the recomputation disagrees, and the caller is told
// `recovery_required` — "these bytes are corrupt". The truth is "this store does
// not implement that algorithm". **Two different facts, one code**, and the
// second one sends the reader to look for damage that does not exist.
//
// The fix is not a new storage format. It is to let a digest *name* its
// algorithm, and to answer with the right fact when it names one this store
// does not have.
const (
	// AlgoSHA256 is the only hash this store implements.
	AlgoSHA256 = "sha256"
	// NormRaw means the bytes were hashed exactly as given. Payload files are
	// hashed this way: they are not JSON and have no normalization.
	NormRaw = "raw"
	// NormCanonJSON means the bytes were passed through core.Canonical first.
	// A manifest's identity depends on it: the same JSON object rendered by a
	// different normalizer hashes to a different digest, and the two are the
	// same *value* under two *identities*.
	NormCanonJSON = "canon-json-v1"
)

// Hash returns the hex sha256 of data. This is the only name a blob has.
//
// The name is bare hex because that is the on-disk and on-the-wire form, and
// changing it would rewrite every ref in every store. A caller that wants to say
// the algorithm out loud writes `sha256:<hex>`, which SplitDigest parses and Get
// accepts. Both forms name the same blob; neither is a second format.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SplitDigest separates a digest into its algorithm and its hex.
//
// A bare 64-hex string is accepted and means sha256: that is what every stored
// hash looks like today, and a parser that refused them would refuse the whole
// store. Anything else must name its algorithm, so an unknown one can be
// answered honestly instead of being mistaken for corruption.
func SplitDigest(d string) (algo, hexsum string) {
	for i := 0; i < len(d); i++ {
		if d[i] == ':' {
			return d[:i], d[i+1:]
		}
	}
	return AlgoSHA256, d
}

// ValidDigest reports whether d is a digest this store can act on.
//
// It returns the algorithm and whether it is one this store implements, so a
// caller can tell "malformed" from "well-formed but not mine". Those are two
// different answers and collapsing them is the defect this file is about.
func ValidDigest(d string) (algo, hexsum string, ok bool) {
	algo, hexsum = SplitDigest(d)
	if algo != AlgoSHA256 {
		return algo, hexsum, false
	}
	return algo, hexsum, ValidHash(hexsum)
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
func (c *CAS) Get(digest string) ([]byte, error) {
	algo, hexsum, ok := ValidDigest(digest)
	if algo != AlgoSHA256 {
		// Well-formed but not mine. This is a caller error, not damage: the
		// bytes under this name may be perfectly fine, and telling the reader to
		// run recovery would send them looking for a problem that is not there.
		return nil, &core.Refusal{
			Code: core.CodeBadRequest,
			Reason: "this store implements " + AlgoSHA256 + " and cannot read a " + algo +
				" digest: " + digest + " is not corrupt, it is written in an algorithm this store does not have",
		}
	}
	if !ok {
		return nil, &core.Refusal{Code: core.CodeBadRequest, Reason: "not a content hash: " + digest}
	}
	data, err := os.ReadFile(c.path(hexsum))
	if err != nil {
		return nil, err
	}
	// Verify on read. A store that hands back bytes it cannot vouch for is not
	// content-addressed — it is merely content-labelled.
	if got := Hash(data); got != hexsum {
		return nil, &core.Refusal{
			Code:   core.CodeRecoveryRequired,
			Reason: hexsum + " holds bytes that hash to " + got,
		}
	}
	return data, nil
}

// DEAD-API: CAS 的存在性检查没有调用点
// Has reports presence only. It does not verify the bytes.
//
// It splits the digest first, for the same reason Get does: a store where Get
// accepts a name and Has denies it is a store whose two halves disagree about
// what a name is.
func (c *CAS) Has(digest string) bool {
	algo, hexsum, ok := ValidDigest(digest)
	if algo != AlgoSHA256 || !ok {
		return false
	}
	_, err := os.Stat(c.path(hexsum))
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
//
// ★ It checks the **normalization** too, which is the third leg of the identity
// and the one nothing used to check.
//
// A manifest's digest is the hash of its canonical bytes. If those bytes were
// produced by a different normalizer — an older core.Canonical, another
// implementation of the same idea — they are the same *value* under a different
// *identity*, and the digest no longer means what it says. Re-canonicalizing and
// comparing is how that shows up, and it is a different answer from corruption:
// the bytes are intact, they were just written to a different recipe.
func (c *CAS) GetJSON(hash string, v any) error {
	data, err := c.Get(hash)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	canon, ref := core.Canonical(data)
	if ref != nil {
		return ref
	}
	if string(canon) != string(data) {
		return &core.Refusal{
			Code: core.CodeRecoveryRequired,
			Reason: hash + " is not in " + NormCanonJSON + ": these bytes are intact, but they were " +
				"written to a different normalization, so this digest names a value under an identity " +
				"this store does not share",
		}
	}
	return nil
}
