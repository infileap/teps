package main

import (
	"encoding/json"
	"strings"
	"testing"

	"teps/core"
)

// ★★★ 一个拼错的字段名必须被拒，而不是**换一个操作**。
//
// `decodeBody` 此前用 `json.Unmarshal` —— 它**静默丢掉不认识的字段**。
// 实测（`arbiter.observe`）：
//
//	① 客户端把 outcome 拼成 ok：
//	   {"attempt":"x1","skill":"s.a","task_class":"task_exec","ok":true}
//	   -> {"seq":1,"rev":1,"body":{"head":"cac2ac…","open":1}}   **被当成一次 intent**
//	② 客户端写对了 outcome：
//	   {"attempt":"x1","skill":"s.a","task_class":"task_exec","outcome":"ok"}
//	   -> {"err":{"code":"bad_request","reason":"an outcome needs verified_by: …"}}
//
// 两句请求做的事**完全不同**，而**拼错的那一句成功了**。客户端拿到的是一次
// 成功与一条链上记录，它没有任何办法知道自己问的不是它以为的那件事。
//
// 契约 A3 说「拒绝是一个值，不是异常」。一个拼错的字段名既不是拒绝，
// 也不是它字面上的那个请求 —— 它是一句被悄悄改成别的话的请求。
func TestAMisspelledFieldIsRefusedRatherThanRerouted(t *testing.T) {
	type observe struct {
		Attempt   string `json:"attempt"`
		Skill     string `json:"skill"`
		TaskClass string `json:"task_class"`
		Outcome   string `json:"outcome"`
	}
	// ★ 这是**拼错的那一句**：`ok` 不是这个形状里的字段。
	var got observe
	ref := decodeBody(json.RawMessage(`{"attempt":"x1","skill":"s.a","task_class":"task_exec","ok":true}`), &got)
	if ref == nil {
		t.Fatalf("a misspelled field must be refused, not silently dropped: got %+v", got)
	}
	if ref.Code != core.CodeBadRequest {
		t.Fatalf("want bad_request, got %s", ref.Code)
	}
	// ★ 而理由必须**点名那个字段** —— 只说「解不开」读者无从下手。
	if !strings.Contains(ref.Reason, "ok") {
		t.Fatalf("the reason must name the field it did not understand, got %q", ref.Reason)
	}

	// ★ 反向对照：写对了的那一句必须过。
	var ok observe
	if ref := decodeBody(json.RawMessage(`{"attempt":"x1","skill":"s.a","task_class":"task_exec","outcome":"ok"}`), &ok); ref != nil {
		t.Fatalf("a correct body must decode: %s", ref.Reason)
	}
	if ok.Outcome != "ok" {
		t.Fatalf("outcome = %q", ok.Outcome)
	}
}

// ★ 空 body 是「没给」，不是「给了一个空对象」。
func TestAnEmptyBodyIsStillMissing(t *testing.T) {
	var got struct{}
	ref := decodeBody(nil, &got)
	if ref == nil || !strings.Contains(ref.Reason, "missing body") {
		t.Fatalf("want missing body, got %v", ref)
	}
}

// ★★★ 多出来的第二个 JSON 值也必须拒。
//
// `json.Decoder` 只读第一个值 —— 于是 `{"a":1}{"b":2}` 会被当成一个合法的请求，
// 而第二个值**静默消失**。executor 那边已经为同一条付过学费
// （`json.Decoder` 在读一个流时停在第一个值），这里是同一个坑的另一半。
func TestASecondJSONValueInTheBodyIsRefused(t *testing.T) {
	var got struct {
		Action string `json:"action"`
	}
	ref := decodeBody(json.RawMessage(`{"action":"a"}{"action":"b"}`), &got)
	if ref == nil {
		t.Fatal("a body holding two JSON values must be refused: the first would be read and the rest silently ignored")
	}
	if !strings.Contains(ref.Reason, "more than one JSON value") {
		t.Fatalf("the reason must say what happened, got %q", ref.Reason)
	}
}

// ★ 而整条链路上最该安静的那一个帧不该安静：`spend` 上拼错一个字段名
// 会**照原样花掉一笔钱**。它现在也走 decodeBody。
func TestSpendRefusesAnUnknownField(t *testing.T) {
	s := testServer(t)
	out := s.handle(core.Frame{T: "spend", V: 1,
		Body: json.RawMessage(`{"ms":100,"tokns":5}`)})
	if out.Err == nil {
		t.Fatalf("a misspelled budget field must be refused, not silently dropped: %+v", out)
	}
	if !strings.Contains(out.Err.Reason, "tokns") {
		t.Fatalf("the reason must name the field it did not understand, got %q", out.Err.Reason)
	}
}
