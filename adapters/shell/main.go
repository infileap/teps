// Command shell is the thinnest possible adapter over the kernel.
//
// It owns no state of its own. It reads one frame per line, hands it to the
// kernel, and writes exactly one response line back. The same loop serves
// stdin/stdout and an HTTP endpoint, so the transport is a deployment choice
// rather than a property of the protocol.
//
// A client can drive this with curl alone; nothing here knows about any
// particular host.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"sansheng/core"
	"sansheng/store"
)

// scannerMax is deliberately larger than the protocol cap: a frame that is too
// large must come back as a refusal, not as a dropped connection.
const scannerMax = 8 << 20

type server struct {
	log       *core.Log
	acct      *core.Account
	sessions  *sessionStore
	memory    *memoryStore
	skills    *store.Store
	decisions *decisionStore
	reflexes  *reflexStore
	tasks     *taskStore
}

func main() {
	dir := flag.String("dir", "", "directory holding the chain")
	storeDir := flag.String("store", "", "skill store directory (default <dir>/skills)")
	addr := flag.String("http", "", "listen address, e.g. 127.0.0.1:0")
	ms := flag.Int64("ms", 60000, "initial budget: milliseconds")
	tokens := flag.Int64("tokens", 1000000, "initial budget: tokens")
	money := flag.Int64("money", 1000, "initial budget: money units")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "shell adapter: -dir is required")
		os.Exit(2)
	}

	lg, ref := core.Open(*dir)
	if ref != nil {
		fmt.Fprintln(os.Stderr, "shell adapter: "+ref.Error())
		os.Exit(1)
	}
	defer lg.Close()

	sessions, ref := newSessionStore(lg)
	if ref != nil {
		fmt.Fprintln(os.Stderr, "shell adapter: "+ref.Error())
		os.Exit(1)
	}

	mem, ref := newMemoryStore(lg)
	if ref != nil {
		fmt.Fprintln(os.Stderr, "shell adapter: "+ref.Error())
		os.Exit(1)
	}

	dec, ref := newDecisionStore(lg)
	if ref != nil {
		fmt.Fprintln(os.Stderr, "shell adapter: "+ref.Error())
		os.Exit(1)
	}

	sdir := *storeDir
	if sdir == "" {
		sdir = filepath.Join(*dir, "skills")
	}
	sk, err := store.OpenStore(sdir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shell adapter: "+err.Error())
		os.Exit(1)
	}

	rfx, ref := newReflexStore(lg)
	if ref != nil {
		fmt.Fprintln(os.Stderr, "shell adapter: "+ref.Reason)
		os.Exit(1)
	}

	tsk, ref := newTaskStore(lg)
	if ref != nil {
		fmt.Fprintln(os.Stderr, "shell adapter: "+ref.Reason)
		os.Exit(1)
	}

	s := &server{
		log:       lg,
		acct:      core.NewAccount(core.Budget{MS: *ms, Tokens: *tokens, Money: *money}),
		sessions:  sessions,
		memory:    mem,
		skills:    sk,
		decisions: dec,
		reflexes:  rfx,
		tasks:     tsk,
	}

	if *addr != "" {
		ln, err := net.Listen("tcp", *addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "shell adapter: "+err.Error())
			os.Exit(1)
		}
		// The resolved address goes to stderr so stdout stays a pure protocol
		// stream even when the port was chosen by the operating system.
		fmt.Fprintf(os.Stderr, "LISTEN %s\n", ln.Addr().String())
		mux := http.NewServeMux()
		mux.HandleFunc("/k", s.httpHandler)
		go func() { _ = http.Serve(ln, mux) }()
	}

	out := bufio.NewWriter(os.Stdout)
	serve(os.Stdin, out, s)
	_ = out.Flush()

	// With a listener open, end of stdin is not a reason to stop serving.
	if *addr != "" {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
	}
}

// serve is the whole protocol loop, independent of which side of a wire it is
// running on.
func serve(r io.Reader, w io.Writer, s *server) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), scannerMax)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var out core.Frame
		if f, ref := core.ParseFrame(line); ref != nil {
			out = core.Frame{T: "refused", V: core.ProtocolVersion, Err: ref}
		} else {
			out = s.handle(f)
		}
		_, _ = w.Write(out.Marshal())
		if fl, ok := w.(interface{ Flush() error }); ok {
			_ = fl.Flush()
		}
		if out.T == "bye" {
			return
		}
	}
}

func (s *server) httpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, core.MaxFrameBytes+1))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	serve(bytes.NewReader(body), w, s)
}

func (s *server) handle(f core.Frame) core.Frame {
	out := core.Frame{T: f.T, V: core.ProtocolVersion, ID: f.ID}

	if strings.HasPrefix(f.T, "session.") {
		if s.handleSession(f, &out) {
			return out
		}
	}

	// skill.invoke must be claimed before handleSkill: the skill store owns
	// skill.publish/resolve/list/promote and would answer this one with
	// "unknown frame type". The dispatcher stays the only place that knows
	// which handler owns which frame type.
	if s.handleExecute(f, &out) {
		return out
	}

	if strings.HasPrefix(f.T, "skill.") {
		if s.handleSkill(f, &out) {
			return out
		}
	}

	if s.handleKnowledge(f, &out) {
		return out
	}

	if s.handleDistill(f, &out) {
		return out
	}
	if s.handleIntake(f, &out) {
		return out
	}
	if s.handleEscalation(f, &out) {
		return out
	}
	if s.handleReflex(f, &out) {
		return out
	}
	if s.handleDecision(f, &out) {
		return out
	}

	switch f.T {
	case "hello":
		out.T = "welcome"
		out.Body = mustJSON(map[string]any{
			"epoch":   s.log.Epoch(),
			"version": core.ProtocolVersion,
		})

	case "put":
		e, ref := s.log.Append(f.Key, f.Body)
		if ref != nil {
			out.Err = ref
			return out
		}
		out.Seq = e.Seq
		out.Rev = e.Seq
		out.Body = mustJSON(map[string]any{"seq": e.Seq, "head": e.Head, "key": e.Key})

	case "spend":
		var want core.Budget
		if len(f.Body) > 0 {
			if err := json.Unmarshal(f.Body, &want); err != nil {
				out.Err = core.Refuse(core.CodeBadRequest, err.Error())
				return out
			}
		}
		if ref := s.acct.Spend(want); ref != nil {
			out.Err = ref
			return out
		}
		out.Body = mustJSON(map[string]any{"left": s.acct.Left()})

	case "status":
		head, seq := s.log.Head()
		out.Body = mustJSON(map[string]any{
			"epoch": s.log.Epoch(),
			"seq":   seq,
			"head":  head,
			"left":  s.acct.Left(),
		})

	case "drain":
		head, seq := s.log.Head()
		out.Body = mustJSON(map[string]any{"drained": true, "seq": seq, "head": head})

	case "bye":
		out.Body = mustJSON(map[string]any{"epoch": s.log.Epoch()})

	default:
		out.Err = core.Refuse(core.CodeBadRequest, "unknown frame type "+f.T)
	}
	return out
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
