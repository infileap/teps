// Command mcp translates the Model Context Protocol into the kernel's NDJSON.
//
// # Why this package imports nothing from the project
//
// An adapter that imports the kernel stops being an adapter and becomes a
// second entry point into it. A second entry point drifts from the first — this
// project has paid for that lesson three times already: a doc table that
// disagreed with the kernel's refusal codes, two functions that gave one fact
// two codes, and an outcome the executor was allowed to certify itself.
//
// So this file imports the standard library and nothing else. It does not know
// what a skill is, what a session is, or which op names exist. It knows how to
// read a JSON-RPC line, how to write an NDJSON line to a child process, and how
// to hand the answer back unaltered. tools/check_adapters.py enforces this.
//
// # Why there is one tool and not thirty
//
// The obvious design is one MCP tool per kernel op. That would mean writing the
// op names down here, and then the op names exist in two places. A translator
// that renames things becomes a second vocabulary, and a second vocabulary
// drifts silently.
//
// So there is exactly one tool, and its input schema is the kernel's own frame
// envelope. The op names stay the kernel's business. The follow-up worth doing
// is for the kernel to grow an op that reports its own op set, so a client can
// discover them without anyone writing them down twice.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

const (
	serverName = "teps-mcp"
	// The kernel's own protocol version. The adapter does not negotiate it; it
	// reports what it speaks and refuses to pretend otherwise.
	adapterVersion = "0.1.0"
)

// rpcRequest is one JSON-RPC 2.0 message. ID is kept raw because the spec allows
// a string or a number, and re-encoding it as one or the other would be the
// adapter editing the client's message.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// child is the kernel process this adapter drives.
//
// The pipe is kept open across calls: spawning per request would pay process
// startup on every tool call, and the kernel's whole point is that a restart is
// a supported operation rather than a failure — so keeping one alive is an
// optimisation, not a correctness requirement.
type child struct {
	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
	epoch uint64
	// restarts counts how many times the child had to be replaced. It is
	// reported on every result, because a caller whose state moved is entitled
	// to know that it moved.
	restarts int
}

var (
	childPath string
	chainDir  string
)

func main() {
	flag.StringVar(&childPath, "child", "", "path to the kernel's reference transport binary")
	flag.StringVar(&chainDir, "dir", "", "directory holding the chain (passed to the child)")
	flag.Parse()

	if childPath == "" || chainDir == "" {
		fmt.Fprintln(os.Stderr, "mcp adapter: -child and -dir are required")
		os.Exit(2)
	}

	c := &child{}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 8<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			write(out, rpcResponse{JSONRPC: "2.0", Error: &rpcError{
				Code: codeParseError, Message: "parse error: " + err.Error(),
			}})
			continue
		}
		// A message with no id is a notification: the spec forbids replying.
		if len(req.ID) == 0 {
			continue
		}
		write(out, c.dispatch(req))
	}
}

func write(w *bufio.Writer, resp rpcResponse) {
	resp.JSONRPC = "2.0"
	b, err := json.Marshal(resp)
	if err != nil {
		b = []byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"unencodable response"}}`)
	}
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
	_ = w.Flush()
}

func (c *child) dispatch(req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": adapterVersion},
		}}

	case "ping":
		return rpcResponse{ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		return rpcResponse{ID: req.ID, Result: map[string]any{"tools": []any{toolDescriptor()}}}

	case "tools/call":
		return c.callTool(req)

	default:
		return rpcResponse{ID: req.ID, Error: &rpcError{
			Code: codeMethodNotFound, Message: "method not found: " + req.Method,
		}}
	}
}

// toolDescriptor describes the one tool.
//
// The schema below is the kernel's frame envelope — t, key, body — written down
// once. It is not a taxonomy of the kernel's operations; that list is not
// reproduced here on purpose.
func toolDescriptor() map[string]any {
	return map[string]any{
		"name": "kernel",
		"description": "Send one frame to the teps kernel and get its answer back verbatim. " +
			"`t` is the kernel's own frame type (for example hello, status, put). " +
			"`key` is an idempotency key: repeating a call with the same key must not apply it twice. " +
			"If you omit it, the adapter derives one from the JSON-RPC id and the parameters, so a " +
			"retried request is deduplicated while two different requests are not. " +
			"`body` is passed through untouched. A refusal comes back with its code, its reason and " +
			"whether it is safe to resend, all three preserved.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"t":    map[string]any{"type": "string", "description": "the kernel's frame type"},
				"key":  map[string]any{"type": "string", "description": "idempotency key; omit to have one derived"},
				"body": map[string]any{"type": "object", "description": "the frame body, passed through untouched"},
			},
			"required": []string{"t"},
		},
	}
}

func (c *child) callTool(req rpcRequest) rpcResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcResponse{ID: req.ID, Error: &rpcError{Code: codeInvalidParams, Message: err.Error()}}
	}
	if params.Name != "kernel" {
		return rpcResponse{ID: req.ID, Error: &rpcError{
			Code: codeInvalidParams, Message: "unknown tool: " + params.Name,
		}}
	}

	// The frame is the caller's arguments, plus only what this adapter owns: the
	// protocol version, and an idempotency key when the caller did not supply one.
	//
	// Deliberately not a whitelist of recognised fields. The kernel refuses any
	// frame that supplies a server-owned field such as seq or head, and an
	// adapter that copied only the fields it understood would make that guard
	// unreachable: it would have silently switched off a safety check it does
	// not understand. A translator that cannot forward something must forward it
	// anyway and let the far side decide.
	var frame map[string]any
	if err := json.Unmarshal(params.Arguments, &frame); err != nil {
		return rpcResponse{ID: req.ID, Error: &rpcError{Code: codeInvalidParams, Message: err.Error()}}
	}
	t, _ := frame["t"].(string)
	if t == "" {
		return rpcResponse{ID: req.ID, Error: &rpcError{
			Code: codeInvalidParams, Message: "the frame needs a type: t",
		}}
	}
	frame["v"] = 1

	// A retried JSON-RPC request is the same logical attempt, so it must reuse
	// the idempotency key. Deriving it from the id alone would collide when a
	// client reuses an id for a different call, so the whole argument set is
	// folded in: same id and same arguments dedup, same id and different
	// arguments do not.
	if _, supplied := frame["key"]; !supplied {
		frame["key"] = derivedKey(req.ID, params.Arguments)
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		return rpcResponse{ID: req.ID, Error: &rpcError{Code: codeInternalError, Message: err.Error()}}
	}

	resp, epoch, restarts, err := c.roundTrip(raw)
	if err != nil {
		// The transport failed, not the kernel. Reporting this as a tool error
		// rather than a JSON-RPC error is deliberate: a JSON-RPC error means the
		// request never reached the kernel, and a caller must be able to tell
		// those apart from a refusal.
		return rpcResponse{ID: req.ID, Result: toolError("transport: "+err.Error(), nil, restarts)}
	}

	// The kernel's answer is handed back as-is. Flattening a refusal into a
	// sentence would lose the code and the retryable flag, and a caller that
	// cannot see whether a refusal is safe to resend will resend the unsafe ones.
	note := map[string]any{"epoch": epoch, "restarts": restarts}
	return rpcResponse{ID: req.ID, Result: toolResult(resp, note)}
}

// toolResult wraps the kernel's raw answer without editing it.
func toolResult(resp json.RawMessage, note map[string]any) map[string]any {
	var parsed any
	if err := json.Unmarshal(resp, &parsed); err != nil {
		parsed = string(resp)
	}
	isErr := false
	if m, ok := parsed.(map[string]any); ok {
		_, isErr = m["err"]
	}
	return map[string]any{
		"content": []any{map[string]any{
			"type": "text",
			"text": string(resp),
		}},
		"isError": isErr,
		"_meta":   note,
	}
}

func toolError(msg string, data any, restarts int) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": msg}},
		"isError": true,
		"_meta":   map[string]any{"restarts": restarts},
	}
}

func derivedKey(id json.RawMessage, args json.RawMessage) string {
	h := sha256.New()
	h.Write(id)
	h.Write([]byte{0})
	h.Write(args)
	return "mcp:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// roundTrip sends one frame and reads exactly one line back.
func (c *child) roundTrip(frame []byte) (json.RawMessage, uint64, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		if c.cmd == nil {
			if err := c.start(); err != nil {
				return nil, 0, c.restarts, err
			}
		}
		if _, err := c.stdin.Write(append(frame, '\n')); err != nil {
			c.stop()
			continue
		}
		line, err := c.out.ReadBytes('\n')
		if err != nil {
			c.stop()
			continue
		}
		return json.RawMessage(trimNewline(line)), c.epoch, c.restarts, nil
	}
	return nil, 0, c.restarts, errors.New("the child did not answer after a restart")
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func (c *child) start() error {
	cmd := exec.Command(childPath, "-dir", chainDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// The child's diagnostics go to stderr, so stdout stays a pure protocol
	// stream. Forwarding it keeps a crash visible instead of silent.
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	c.stdin = stdin
	c.out = bufio.NewReaderSize(stdout, 1<<20)

	// A handshake, so the adapter learns the epoch before it forwards anything.
	// The epoch is how a caller notices that the process it was talking to is
	// not the process it is talking to now.
	hello, err := json.Marshal(map[string]any{"t": "hello", "v": 1})
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(hello, '\n')); err != nil {
		return err
	}
	line, err := c.out.ReadBytes('\n')
	if err != nil {
		return err
	}
	var welcome struct {
		Body struct {
			Epoch uint64 `json:"epoch"`
		} `json:"body"`
	}
	if err := json.Unmarshal(trimNewline(line), &welcome); err != nil {
		return err
	}
	if c.epoch != 0 && welcome.Body.Epoch != c.epoch {
		c.restarts++
	}
	c.epoch = welcome.Body.Epoch
	return nil
}

func (c *child) stop() {
	if c.cmd != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	c.cmd = nil
	c.stdin = nil
	c.out = nil
}
