package main

import (
	"encoding/json"
	"sort"
	"strings"

	"teps/core"
	"teps/store"
)

// The skill ops are the store's protocol face: publish, resolve, list, promote.
//
// Note what is **not** here: any policy. The store owns the license policy, the
// evidence rule and the promotion gate. The adapter only moves bytes and refusal
// values — which is the entire job of an adapter, and the reason it can be
// deleted without the kernel noticing.

type publishBody struct {
	Manifest json.RawMessage   `json:"manifest"`
	Files    map[string]string `json:"files"`
}

// handleSkill returns false when the frame is not a skill op, so the caller can
// fall through to the session ops and then to the core ops.
func (s *server) handleSkill(f core.Frame, out *core.Frame) bool {
	if !strings.HasPrefix(f.T, "skill.") {
		return false
	}
	if s.skills == nil {
		out.Err = core.Refuse(core.CodeBadRequest, "this server was started without a skill store")
		return true
	}
	switch f.T {
	case "skill.publish":
		s.skillPublish(f, out)
	case "skill.resolve":
		s.skillResolve(f, out)
	case "skill.list":
		s.skillList(f, out)
	case "skill.promote":
		s.skillPromote(f, out)
	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return true
}

func (s *server) skillPublish(f core.Frame, out *core.Frame) {
	var body publishBody
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	var m store.Manifest
	if ref := decodeBody(body.Manifest, &m); ref != nil {
		out.Err = ref
		return
	}
	files := make(map[string][]byte, len(body.Files))
	for name, text := range body.Files {
		files[name] = []byte(text)
	}
	res, ref := s.skills.Register(store.Registration{Manifest: &m, Files: files})
	if ref != nil {
		// The refusal is the value; the steps are the diagnosis. Both go back,
		// because "it was refused" is not something a caller can act on.
		out.Err = ref
		out.Body = mustJSON(map[string]any{"steps": res.Steps})
		return
	}
	out.Body = mustJSON(map[string]any{
		"ref":      res.Ref,
		"manifest": res.Manifest,
		"channel":  res.Channel,
		"evidence": string(res.Evidence),
		"license":  res.License,
		"steps":    res.Steps,
	})
}

func (s *server) skillResolve(f core.Frame, out *core.Frame) {
	var body struct {
		Ref string `json:"ref"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	pkg, m, ref := s.skills.Resolve(body.Ref)
	if ref != nil {
		out.Err = ref
		return
	}
	names := make([]string, 0, len(pkg.Files))
	for n := range pkg.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	out.Body = mustJSON(map[string]any{
		"ref":           body.Ref,
		"manifest_hash": pkg.Manifest,
		"manifest":      m,
		"files":         names,
		"license":       pkg.License,
		"evidence":      string(pkg.Evidence),
	})
}

func (s *server) skillList(f core.Frame, out *core.Frame) {
	var body struct {
		Name string `json:"name"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	channels, err := s.skills.Refs.Channels(body.Name)
	if err != nil {
		out.Err = core.Refuse(core.CodeBadRequest, "no such skill: "+body.Name)
		return
	}
	refs := map[string]string{}
	history := map[string][]string{}
	for _, ch := range channels {
		if h, ref := s.skills.Refs.Get(body.Name, ch); ref == nil {
			refs[ch] = h
		}
		if h, ref := s.skills.Refs.History(body.Name, ch); ref == nil {
			history[ch] = h
		}
	}
	out.Body = mustJSON(map[string]any{
		"name": body.Name, "channels": channels, "refs": refs, "history": history,
	})
}

func (s *server) skillPromote(f core.Frame, out *core.Frame) {
	var body struct {
		Name string `json:"name"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if ref := decodeBody(f.Body, &body); ref != nil {
		out.Err = ref
		return
	}
	// from/to are required, with no defaults. A default here would be exactly
	// the thing this project refuses elsewhere: a field nobody filled in
	// becoming a decision nobody checked.
	if body.Name == "" || body.From == "" || body.To == "" {
		out.Err = core.Refuse(core.CodeBadRequest, "name, from and to are all required")
		return
	}
	if ref := s.skills.Promote(body.Name, body.From, body.To); ref != nil {
		out.Err = ref
		return
	}
	h, _ := s.skills.Refs.Get(body.Name, body.To)
	out.Body = mustJSON(map[string]any{"name": body.Name, "channel": body.To, "ref": h})
}

func decodeBody(raw json.RawMessage, v any) *core.Refusal {
	if len(raw) == 0 {
		return core.Refuse(core.CodeBadRequest, "missing body")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return core.Refuse(core.CodeBadRequest, err.Error())
	}
	return nil
}
