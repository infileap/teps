package core

import (
	"bytes"
	"encoding/json"
)

const (
	// ProtocolVersion is bumped whenever the envelope changes shape.
	ProtocolVersion = 1

	// MaxFrameBytes caps one frame. A frame is one line of NDJSON; the cap is
	// on the line, not on the stream.
	MaxFrameBytes = 1 << 20
)

// A1: the server owns the position.
//
// These field names are never accepted from a client. Sending any of them is a
// refusal, not a value to be overwritten, because a client that believes it can
// set the position will eventually set it wrong.
var serverOwned = []string{"seq", "rev", "head", "balance", "state_hash", "committed"}

// Frame is the one envelope every message shares, whatever it carries.
//
// T is the message type, V the protocol version, ID a client-chosen
// correlation token that is echoed back untouched. Seq and Rev are assigned by
// the server; a client that supplies them is refused.
type Frame struct {
	T    string          `json:"t"`
	V    int             `json:"v"`
	ID   string          `json:"id,omitempty"`
	Key  string          `json:"key,omitempty"`
	Seq  uint64          `json:"seq,omitempty"`
	Rev  uint64          `json:"rev,omitempty"`
	Err  *Refusal        `json:"err,omitempty"`
	Body json.RawMessage `json:"body,omitempty"`
}

// ParseFrame turns one line of NDJSON into a Frame, or refuses it.
//
// The server-owned check runs on the raw object before the typed decode, so a
// field the server owns is refused even when its value would have been
// harmless.
func ParseFrame(raw []byte) (Frame, *Refusal) {
	var f Frame
	if len(raw) > MaxFrameBytes {
		return f, Refuse(CodeFrameTooLarge, "frame exceeds the 1 MiB line cap")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return f, Refuse(CodeBadRequest, "empty frame")
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return f, Refuse(CodeBadRequest, "frame is not a JSON object: "+err.Error())
	}
	for _, name := range serverOwned {
		if _, present := probe[name]; present {
			return f, Refuse(CodeClientAuthority, "client supplied the server-owned field "+name)
		}
	}

	if err := json.Unmarshal(raw, &f); err != nil {
		return f, Refuse(CodeBadRequest, err.Error())
	}
	if f.T == "" {
		return f, Refuse(CodeBadRequest, "frame has no type")
	}
	if f.V != 0 && f.V != ProtocolVersion {
		return f, Refuse(CodeBadRequest, "unsupported protocol version")
	}
	f.V = ProtocolVersion
	return f, nil
}

// Marshal renders a Frame as one NDJSON line, newline included.
func (f Frame) Marshal() []byte {
	b, err := json.Marshal(f)
	if err != nil {
		fallback, _ := json.Marshal(Frame{
			T:   "refused",
			V:   ProtocolVersion,
			Err: Refuse(CodeBadRequest, "response could not be rendered"),
		})
		return append(fallback, '\n')
	}
	return append(b, '\n')
}

// Canonical normalizes arbitrary JSON into one stable byte string.
//
// Two encodings that mean the same thing must hash the same, so the chain
// records the normalized form rather than whatever the client happened to send.
// Object keys come out sorted and numbers keep their literal spelling.
func Canonical(raw []byte) ([]byte, *Refusal) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("null"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, Refuse(CodeBadRequest, "body is not JSON: "+err.Error())
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, Refuse(CodeBadRequest, "body cannot be normalized: "+err.Error())
	}
	return out, nil
}
