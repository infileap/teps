package store

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sansheng/core"
)

// Channels. A new skill lands in canary first; everything else resolves through
// stable. Promotion is a pointer move — and so is rollback, which is the whole
// reason the two halves of the store are separate.
const (
	ChannelCanary = "canary"
	ChannelStable = "stable"
)

// ValidName reports whether n is safe to use as a path element.
//
// Same boundary as ValidHash, for the same reason: names arrive from the wire,
// and "validate then join" is how a name becomes a path traversal.
func ValidName(n string) bool {
	if n == "" || len(n) > 64 {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	switch n[0] {
	case '.', '_', '-':
		return false
	}
	return true
}

// Refs is the mutable half of the store: named pointers *into* the immutable CAS.
//
// Nothing here is content. A ref can be moved and moved back, which is exactly
// what "instant rollback" means — and it is why these are pointers in a
// separate tree instead of a "current" symlink living among the blobs.
type Refs struct{ root string }

// OpenRefs opens (creating if needed) a ref tree rooted at dir.
func OpenRefs(dir string) (*Refs, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Refs{root: dir}, nil
}

func (r *Refs) dir(name string) string     { return filepath.Join(r.root, name) }
func (r *Refs) ptr(name, ch string) string { return filepath.Join(r.root, name, ch) }
func (r *Refs) hist(name, ch string) string {
	return filepath.Join(r.root, name, ch+".log")
}

// Get returns the hash a ref currently points at.
func (r *Refs) Get(name, channel string) (string, *core.Refusal) {
	if !ValidName(name) || !ValidName(channel) {
		return "", &core.Refusal{Code: core.CodeBadRequest, Reason: "bad ref name: " + name + "/" + channel}
	}
	data, err := os.ReadFile(r.ptr(name, channel))
	if err != nil {
		// No dedicated "not found" code. The codes are about what a client can
		// DO differently, and "not found" has the same remedy as "malformed" —
		// fix the request. The reason string carries the distinction.
		return "", &core.Refusal{Code: core.CodeBadRequest, Reason: "no such ref: " + name + "/" + channel}
	}
	h := strings.TrimSpace(string(data))
	if !ValidHash(h) {
		return "", &core.Refusal{
			Code:   core.CodeRecoveryRequired,
			Reason: "ref " + name + "/" + channel + " does not hold a content hash",
		}
	}
	return h, nil
}

// Set points a ref at a hash and appends to its history.
//
// Order is deliberate: **history first, then the pointer moves.**
// A crash between the two leaves a history entry for a switch that never
// landed — benign, and visible. The reverse order would allow a switch with no
// record at all, which is the failure that makes rollback unauditable.
// It is the same intent/resolution split as the execution record, kept cheap
// because a ref is not a claim about the world.
func (r *Refs) Set(name, channel, hash string) *core.Refusal {
	if !ValidName(name) || !ValidName(channel) {
		return &core.Refusal{Code: core.CodeBadRequest, Reason: "bad ref name: " + name + "/" + channel}
	}
	if !ValidHash(hash) {
		return &core.Refusal{Code: core.CodeBadRequest, Reason: "a ref must point at a content hash: " + hash}
	}
	// Moving a pointer to where it already points is not a change. Recording it
	// as one would make the history lie about how many times the skill moved,
	// and this early return is what makes publishing the same content twice
	// idempotent: the second publish produces the same hash, so nothing moves.
	if cur, ref := r.Get(name, channel); ref == nil && cur == hash {
		return nil
	}
	if err := os.MkdirAll(r.dir(name), 0o755); err != nil {
		return &core.Refusal{Code: core.CodeBadRequest, Reason: err.Error()}
	}
	f, err := os.OpenFile(r.hist(name, channel), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return &core.Refusal{Code: core.CodeBadRequest, Reason: err.Error()}
	}
	if _, err := f.WriteString(hash + "\t" + time.Now().UTC().Format(time.RFC3339) + "\n"); err != nil {
		f.Close()
		return &core.Refusal{Code: core.CodeBadRequest, Reason: err.Error()}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return &core.Refusal{Code: core.CodeBadRequest, Reason: err.Error()}
	}
	f.Close()

	// Pointer move: temp + rename, so a reader never observes a half-written hash.
	tmp := r.ptr(name, channel) + ".tmp"
	if err := os.WriteFile(tmp, []byte(hash+"\n"), 0o644); err != nil {
		return &core.Refusal{Code: core.CodeBadRequest, Reason: err.Error()}
	}
	if err := os.Rename(tmp, r.ptr(name, channel)); err != nil {
		return &core.Refusal{Code: core.CodeBadRequest, Reason: err.Error()}
	}
	return nil
}

// History returns every hash this ref has pointed at, oldest first.
func (r *Refs) History(name, channel string) ([]string, *core.Refusal) {
	if !ValidName(name) || !ValidName(channel) {
		return nil, &core.Refusal{Code: core.CodeBadRequest, Reason: "bad ref name"}
	}
	f, err := os.Open(r.hist(name, channel))
	if err != nil {
		return nil, &core.Refusal{Code: core.CodeBadRequest, Reason: "no history for " + name + "/" + channel}
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '\t'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if ValidHash(line) {
			out = append(out, line)
		}
	}
	return out, nil
}

// Channels lists the channels defined for a name.
func (r *Refs) Channels(name string) ([]string, error) {
	if !ValidName(name) {
		return nil, nil
	}
	ents, err := os.ReadDir(r.dir(name))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.HasSuffix(n, ".log") || strings.HasSuffix(n, ".tmp") {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}
