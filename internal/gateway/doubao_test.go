package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	doubao "github.com/linguo2625469/workbuddy2api-panel/internal/doubaowork"
)

const doubaoTestModel = "doubao/person-kimi-k3-1-pro-261111"
const doubaoToolReply = `{"type":"tool_calls","tool_calls":[{"name":"echo","arguments":"{\"text\":\"OK\"}"}],"content":null}`

func doubaoToolRequest() map[string]any {
	return map[string]any{
		"model":    doubaoTestModel,
		"messages": []any{map[string]any{"role": "user", "content": "Call echo with text OK."}},
		"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "echo", "parameters": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}}}},
	}
}

func doubaoTestBlock(w http.ResponseWriter, event, blockID string, kind int, text string) {
	sendEvent(w, event, map[string]any{"user_type": 2, "content_block": []any{map[string]any{"block_type": kind, "block_id": blockID, "content": map[string]any{"text_block": map[string]any{"text": text}}}}})
}

func TestDoubaoToolRoundTrip(t *testing.T) {
	turn := 0
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		turn++
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		message := obj(arr(payload["messages"])[0])
		prompt := str(obj(obj(obj(arr(message["content_block"])[0])["content"])["text_block"])["text"])
		if !strings.Contains(prompt, "Available functions:") || !strings.Contains(prompt, `"name":"echo"`) {
			t.Error("function definitions missing from prompt")
		}
		if turn == 1 {
			doubaoTestBlock(w, "FULL_MSG_NOTIFY", "reason", 10040, "I should request echo.")
			reply := `{"role":"assistant","tool_calls":[{"id":"model-call","type":"function","function":{"name":"echo","arguments":{"text":"OK"}}}],"content":null}`
			doubaoTestBlock(w, "STREAM_CHUNK", "answer", 10000, reply[:35])
			sendEvent(w, "CHUNK_DELTA", map[string]any{"text": reply[35:]})
		} else {
			if !strings.Contains(prompt, `"tool_call_id":"call_`) || !strings.Contains(prompt, `"content":"OK"`) {
				t.Error("paired tool history missing")
			}
			_, serialized, found := strings.Cut(prompt, "External conversation (JSON data):\n")
			var transcript struct {
				Messages []map[string]any `json:"messages"`
			}
			if !found || json.NewDecoder(strings.NewReader(serialized)).Decode(&transcript) != nil || len(transcript.Messages) != 3 {
				t.Error("serialized tool history is invalid")
			} else {
				historyCall := obj(arr(transcript.Messages[1]["tool_calls"])[0])
				if str(historyCall["name"]) != "echo" || str(obj(historyCall["arguments"])["text"]) != "OK" || historyCall["id"] != transcript.Messages[2]["tool_call_id"] {
					t.Error("tool arguments or result identity changed during serialization")
				}
			}
			doubaoTestBlock(w, "FULL_MSG_NOTIFY", "answer", 10000, "Finished: OK")
		}
		sendEvent(w, "SSE_REPLY_END", map[string]any{"end_type": 3})
	}, Account{ID: "account"})
	request := doubaoToolRequest()
	request["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "echo"}}
	body, _ := json.Marshal(request)
	var completion Completion
	err := r.Chat(context.Background(), body, "test", func(chunk map[string]any) error {
		for _, raw := range arr(chunk["choices"]) {
			if str(obj(obj(raw)["delta"])["content"]) != "" {
				t.Error("tool envelope leaked as text")
			}
		}
		return completion.Add(chunk)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	choice := obj(arr(completion.Response()["choices"])[0])
	message := obj(choice["message"])
	calls := arr(message["tool_calls"])
	if str(choice["finish_reason"]) != "tool_calls" || len(calls) != 1 || message["content"] != nil || str(message["reasoning_content"]) != "I should request echo." {
		t.Fatalf("invalid completion: %#v", choice)
	}
	call := obj(calls[0])
	if str(obj(call["function"])["arguments"]) != `{"text":"OK"}` || !strings.HasPrefix(str(call["id"]), "call_") {
		t.Fatalf("invalid call: %#v", call)
	}
	request["tool_choice"] = "auto"
	request["messages"] = append(arr(request["messages"]), message, map[string]any{"role": "tool", "tool_call_id": call["id"], "content": "OK"})
	body, _ = json.Marshal(request)
	var final Completion
	if err := r.Chat(context.Background(), body, "test", final.Add, nil); err != nil {
		t.Fatal(err)
	}
	choice = obj(arr(final.Response()["choices"])[0])
	if str(obj(choice["message"])["content"]) != "Finished: OK" || str(choice["finish_reason"]) != "stop" || turn != 2 {
		t.Fatalf("invalid final response: %#v", choice)
	}
}

func TestDoubaoToolValidation(t *testing.T) {
	for _, tc := range []struct {
		name, reply, choice string
		parallel            bool
		valid               bool
	}{
		{"call", doubaoToolReply, "auto", true, true},
		{"ordinary text", "OK", "auto", true, true},
		{"ordinary JSON", `{"value":42}`, "auto", true, true},
		{"required", "OK", "required", true, false},
		{"unknown function", strings.ReplaceAll(doubaoToolReply, "echo", "shell"), "auto", true, false},
		{"invalid args", `{"type":"tool_calls","tool_calls":[{"name":"echo","arguments":"[]"}]}`, "auto", true, false},
		{"missing calls", `{"type":"tool_calls","tool_calls":[]}`, "auto", true, false},
		{"truncated", `{"type":"tool_calls","tool_calls":[`, "auto", true, false},
		{"extra fields", `{"type":"tool_calls","tool_calls":[{"name":"echo","arguments":"{}","execute":true}]}`, "auto", true, false},
		{"nested extra fields", `{"tool_calls":[{"function":{"name":"echo","arguments":{},"execute":true}}]}`, "auto", true, false},
		{"mixed fields", `{"tool_calls":[{"name":"echo","function":{"name":"echo","arguments":{}}}]}`, "auto", true, false},
		{"null arguments", `{"tool_calls":[{"name":"echo","arguments":null}]}`, "auto", true, false},
		{"array arguments", `{"tool_calls":[{"name":"echo","arguments":[]}]}`, "auto", true, false},
		{"wrong call type", `{"tool_calls":[{"type":"shell","function":{"name":"echo","arguments":{}}}]}`, "auto", true, false},
		{"wrong role", `{"role":"user","tool_calls":[{"name":"echo","arguments":{}}]}`, "auto", true, false},
		{"wrong envelope type", `{"type":"other","tool_calls":[{"name":"echo","arguments":{}}]}`, "auto", true, false},
		{"invalid call content", `{"type":"tool_calls","tool_calls":[{"name":"echo","arguments":{},"content":{"execute":true}}]}`, "auto", true, false},
		{"parallel disabled", `{"type":"tool_calls","tool_calls":[{"name":"echo","arguments":"{}"},{"name":"echo","arguments":"{}"}]}`, "auto", false, false},
		{"fenced", "```json\n" + doubaoToolReply + "\n```", "auto", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := doubaoToolRequest()
			request["tool_choice"], request["parallel_tool_calls"] = tc.choice, tc.parallel
			bridge, err := newDoubaoTools(request)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = bridge.response(tc.reply)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	request := doubaoToolRequest()
	request["tool_choice"] = "none"
	bridge, _ := newDoubaoTools(request)
	if bridge.active() || !strings.Contains(bridge.instructions(), "Do not request") {
		t.Fatal("none must disable calls")
	}
}

func TestDoubaoToolCallShapes(t *testing.T) {
	for _, tc := range []struct{ name, reply, arguments string }{
		{"flat string", doubaoToolReply, `{"text":"OK"}`},
		{"flat object", `{"type":"tool_calls","tool_calls":[{"name":"echo","arguments": {"text":"OK"}}]}`, `{"text":"OK"}`},
		{"call content null", `{"type":"tool_calls","tool_calls":[{"name":"echo","arguments":"{\"text\":\"OK\"}","content":null}]}`, `{"text":"OK"}`},
		{"nested string", `{"type":"tool_calls","tool_calls":[{"type":"function","id":"upstream","function":{"name":"echo","arguments":"{\"text\":\"OK\"}"}}]}`, `{"text":"OK"}`},
		{"nested object", `{"role":"assistant","tool_calls":[{"index":0,"id":"upstream","type":"function","function":{"name":"echo","arguments":{"text":"OK"}}}],"content":null}`, `{"text":"OK"}`},
		{"number precision", `{"tool_calls":[{"name":"echo","arguments": { "id": 9007199254740993, "ratio":1.234567890123456789 }}]}`, `{"id":9007199254740993,"ratio":1.234567890123456789}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge, _ := newDoubaoTools(doubaoToolRequest())
			content, calls, err := bridge.response(tc.reply)
			if err != nil || len(calls) != 1 || content != "" {
				t.Fatalf("unexpected response: content=%q calls=%#v err=%v", content, calls, err)
			}
			call := obj(calls[0])
			fn := obj(call["function"])
			if str(fn["name"]) != "echo" || str(fn["arguments"]) != tc.arguments || !strings.HasPrefix(str(call["id"]), "call_") || str(call["id"]) == "upstream" {
				t.Fatalf("incorrect normalized call: %#v", call)
			}
		})
	}
	request := doubaoToolRequest()
	request["tools"] = append(arr(request["tools"]), map[string]any{"type": "function", "function": map[string]any{"name": "other"}})
	request["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "echo"}}
	bridge, _ := newDoubaoTools(request)
	if _, _, err := bridge.response(`{"tool_calls":[{"function":{"name":"other","arguments":{}}}]}`); err == nil {
		t.Fatal("nested call ignored forced function selection")
	}
	content, calls, err := bridge.response(`{"type":"tool_calls","content":"Starting.","tool_calls":[{"name":"echo","arguments":{"text":"OK"},"content":"Checking output."}]}`)
	if err != nil || content != "Starting.\n\nChecking output." || len(calls) != 1 {
		t.Fatalf("per-call text was lost: content=%q calls=%d err=%v", content, len(calls), err)
	}
}

func TestDoubaoToolFormatFailureDoesNotCoolAccount(t *testing.T) {
	requests := 0
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		requests++
		reply := doubaoToolReply
		if requests == 1 {
			reply = `{"type":"tool_calls","tool_calls":[{"name":"undeclared","arguments":{}}]}`
		}
		doubaoTestBlock(w, "STREAM_CHUNK", "answer", 10000, reply)
		sendEvent(w, "SSE_REPLY_END", map[string]any{"end_type": 3})
	}, Account{ID: "account"})
	body, _ := json.Marshal(doubaoToolRequest())
	err := r.Chat(context.Background(), body, "", func(chunk map[string]any) error {
		if obj(obj(arr(chunk["choices"])[0])["delta"])["tool_calls"] != nil {
			t.Error("invalid tool call escaped validation")
		}
		return nil
	}, nil)
	if Status(err) != 502 {
		t.Fatalf("expected tool response failure, got %v", err)
	}
	var c Completion
	if err := r.Chat(context.Background(), body, "", c.Add, nil); err != nil {
		t.Fatalf("format error cooled the account: %v", err)
	}
	if requests != 2 || len(arr(obj(obj(arr(c.Response()["choices"])[0])["message"])["tool_calls"])) != 1 {
		t.Fatal("subsequent tool request did not succeed")
	}
}

func TestDoubaoInvalidRequestsNeverReachUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"tool choice", func(r map[string]any) { r["tool_choice"] = "unknown" }},
		{"unknown forced name", func(r map[string]any) {
			r["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "shell"}}
		}},
		{"invalid tools", func(r map[string]any) { r["tools"] = "tools" }},
		{"strict schema", func(r map[string]any) { obj(obj(arr(r["tools"])[0])["function"])["strict"] = true }},
		{"bad parallel", func(r map[string]any) { r["parallel_tool_calls"] = "false" }},
		{"orphan result", func(r map[string]any) {
			r["messages"] = []any{map[string]any{"role": "tool", "tool_call_id": "unknown", "content": "OK"}}
		}},
		{"missing result", func(r map[string]any) {
			r["messages"] = []any{map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "old", "type": "function", "function": map[string]any{"name": "echo", "arguments": "{}"}}}}}
		}},
		{"image", func(r map[string]any) {
			obj(arr(r["messages"])[0])["content"] = []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/image.png"}}}
		}},
		{"structured output", func(r map[string]any) { r["response_format"] = map[string]any{"type": "json_object"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRouter(t, "doubao", func(http.ResponseWriter, *http.Request) { t.Error("invalid request reached upstream") }, Account{ID: "account"})
			request := doubaoToolRequest()
			tc.mutate(request)
			body, _ := json.Marshal(request)
			if err := r.Chat(context.Background(), body, "", func(map[string]any) error { return nil }, nil); Status(err) != 400 {
				t.Fatalf("expected 400: %v", err)
			}
		})
	}
}

func TestDoubaoIncompleteToolResponse(t *testing.T) {
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		io.Copy(io.Discard, req.Body)
		doubaoTestBlock(w, "STREAM_CHUNK", "answer", 10000, doubaoToolReply)
	}, Account{ID: "account"})
	body, _ := json.Marshal(doubaoToolRequest())
	err := r.Chat(context.Background(), body, "", func(chunk map[string]any) error {
		for _, raw := range arr(chunk["choices"]) {
			delta := obj(obj(raw)["delta"])
			if delta["tool_calls"] != nil || delta["content"] != nil {
				t.Error("incomplete tool response was emitted")
			}
		}
		return nil
	}, nil)
	if Status(err) != 502 {
		t.Fatalf("expected 502: %v", err)
	}
}

func TestDoubaoTextAndEchoFiltering(t *testing.T) {
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		sendEvent(w, "FULL_MSG_NOTIFY", map[string]any{"user_type": 1, "content_block": []any{map[string]any{"block_type": 10000, "content": map[string]any{"text_block": map[string]any{"text": "echoed prompt"}}}}})
		doubaoTestBlock(w, "STREAM_CHUNK", "answer", 10000, "OK")
		sendEvent(w, "SSE_REPLY_END", map[string]any{"end_type": 3})
	}, Account{ID: "account"})
	request := doubaoToolRequest()
	delete(request, "tools")
	request["response_format"] = map[string]any{"type": "text"}
	body, _ := json.Marshal(request)
	var c Completion
	if err := r.Chat(context.Background(), body, "", c.Add, nil); err != nil {
		t.Fatal(err)
	}
	if str(obj(obj(arr(c.Response()["choices"])[0])["message"])["content"]) != "OK" {
		t.Fatal("user echo was forwarded")
	}
}

func doubaoPatch(w http.ResponseWriter, patchType int, blocks ...any) {
	sendEvent(w, "STREAM_CHUNK", map[string]any{"patch_op": []any{map[string]any{"patch_type": patchType, "patch_value": map[string]any{"content_block": blocks}}}})
}

func doubaoBlock(blockID, parent, text string, kind int) map[string]any {
	return map[string]any{"block_id": blockID, "parent_id": parent, "block_type": kind, "content": map[string]any{"text_block": map[string]any{"text": text}}}
}

func TestDoubaoMixedToolStream(t *testing.T) {
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		// The thinking container may follow its child in the same patch.
		doubaoPatch(w, 2, doubaoBlock("thinking-text", "thinking", "", 10000), doubaoBlock("thinking", "", "", 10040))
		// A metadata-only container update must not take ownership of bare deltas.
		doubaoPatch(w, 2, map[string]any{"block_id": "thinking", "block_type": 10040, "content": map[string]any{"thinking_block": map[string]any{}}})
		sendEvent(w, "CHUNK_DELTA", map[string]any{"text": "Need echo."})
		doubaoPatch(w, 2, doubaoBlock("answer", "", "", 10000))
		doubaoPatch(w, 1, doubaoBlock("answer", "", doubaoToolReply[:9], 10000))
		sendEvent(w, "CHUNK_DELTA", map[string]any{"text": doubaoToolReply[9:30]})
		doubaoPatch(w, 1, doubaoBlock("answer", "", doubaoToolReply[30:75], 10000))
		sendEvent(w, "CHUNK_DELTA", map[string]any{"text": doubaoToolReply[75:]})
		// Both replacement patches and full-message snapshots can repeat text.
		doubaoPatch(w, 2, doubaoBlock("answer", "", doubaoToolReply, 10000))
		encoded, _ := json.Marshal([]any{doubaoBlock("answer", "", doubaoToolReply, 10000)})
		sendEvent(w, "FULL_MSG_NOTIFY", map[string]any{"message": map[string]any{"user_type": 2, "content": string(encoded)}})
		sendEvent(w, "SSE_REPLY_END", map[string]any{"end_type": 3})
	}, Account{ID: "account"})
	request := doubaoToolRequest()
	request["tool_choice"] = "required"
	body, _ := json.Marshal(request)
	var c Completion
	if err := r.Chat(context.Background(), body, "", c.Add, nil); err != nil {
		t.Fatal(err)
	}
	choice := obj(arr(c.Response()["choices"])[0])
	message := obj(choice["message"])
	calls := arr(message["tool_calls"])
	if len(calls) != 1 || str(choice["finish_reason"]) != "tool_calls" || message["content"] != nil || str(message["reasoning_content"]) != "Need echo." {
		t.Fatalf("invalid completion: %#v", choice)
	}
	if str(obj(obj(calls[0])["function"])["arguments"]) != `{"text":"OK"}` {
		t.Fatalf("truncated tool arguments: %#v", calls[0])
	}
}

func TestDoubaoTextSnapshotsAndMessageIdentity(t *testing.T) {
	p := newDoubaoTextParser()
	var text strings.Builder
	feed := func(event string, frame map[string]any) {
		t.Helper()
		parts, err := p.feed(event, frame)
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range parts {
			if part.field != "content" {
				t.Fatalf("answer was classified as thinking: %#v", part)
			}
			text.WriteString(part.text)
		}
	}
	feed("STREAM_CHUNK", map[string]any{"message_id": "one", "content_block": []any{doubaoBlock("shared", "", "ha", 10000)}})
	feed("CHUNK_DELTA", map[string]any{"text": "ha"}) // Repeated append text is meaningful.
	feed("FULL_MSG_NOTIFY", map[string]any{"message": map[string]any{"user_type": 1, "message_id": "user", "content_block": []any{doubaoBlock("shared", "", "echo", 10000)}}})
	feed("STREAM_CHUNK", map[string]any{"patch_op": []any{map[string]any{"patch_type": 2, "patch_value": map[string]any{"content_block": []any{doubaoBlock("shared", "", "haha!", 10000)}}}}})
	feed("FULL_MSG_NOTIFY", map[string]any{"content_block": []any{doubaoBlock("shared", "", "haha", 10000)}})
	feed("STREAM_CHUNK", map[string]any{"message_id": "two", "content_block": []any{doubaoBlock("shared", "", "OK", 10000)}})
	if text.String() != "haha!OK" {
		t.Fatalf("snapshots/deltas were duplicated or lost: %q", text.String())
	}
	if _, err := p.feed("FULL_MSG_NOTIFY", map[string]any{"content_block": []any{doubaoBlock("shared", "", "different", 10000)}}); Status(err) != 502 {
		t.Fatalf("conflicting revision was accepted: %v", err)
	}
}

func TestDoubaoInvalidUpstreamParametersDoNotCoolAccount(t *testing.T) {
	requests := 0
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		requests++
		if requests == 1 {
			sendEvent(w, "STREAM_ERROR", map[string]any{"error_code": 710020202, "error_msg": "common invalid param"})
			return
		}
		doubaoTestBlock(w, "STREAM_CHUNK", "answer", 10000, "OK")
		sendEvent(w, "SSE_REPLY_END", map[string]any{"end_type": 3})
	}, Account{ID: "account"})
	request := doubaoToolRequest()
	delete(request, "tools")
	body, _ := json.Marshal(request)
	if err := r.Chat(context.Background(), body, "", func(map[string]any) error { return nil }, nil); Status(err) != 400 || Code(err) != "upstream_error" {
		t.Fatalf("expected actionable request error: %v", err)
	}
	var c Completion
	if err := r.Chat(context.Background(), body, "", c.Add, nil); err != nil {
		t.Fatalf("invalid parameters cooled the account: %v", err)
	}
	if requests != 2 || str(obj(obj(arr(c.Response()["choices"])[0])["message"])["content"]) != "OK" {
		t.Fatal("fresh request did not succeed")
	}
}

func TestDoubaoWorkInputLimit(t *testing.T) {
	for _, tc := range []struct {
		name, unit string
		characters int
		wantStatus int
	}{
		{"ASCII boundary", "x", doubao.WorkMaxInputCharacters - 6, 200},
		{"ASCII overflow", "x", doubao.WorkMaxInputCharacters - 5, 400},
		{"Unicode byte size", "中", 100000, 200},
		{"Unicode boundary", "中", doubao.WorkMaxInputCharacters - 6, 200},
		{"Unicode overflow", "中", doubao.WorkMaxInputCharacters - 5, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := 0
			r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
				upstream++
				doubaoTestBlock(w, "STREAM_CHUNK", "answer", 10000, "OK")
				sendEvent(w, "SSE_REPLY_END", map[string]any{"end_type": 3})
			}, Account{ID: "account"})
			request := map[string]any{"model": doubaoTestModel, "messages": []any{map[string]any{"role": "user", "content": strings.Repeat(tc.unit, tc.characters)}}}
			body, _ := json.Marshal(request)
			err := r.Chat(context.Background(), body, "", func(map[string]any) error { return nil }, nil)
			if tc.wantStatus == 200 {
				if err != nil || upstream != 1 {
					t.Fatalf("valid input rejected: upstream=%d err=%v", upstream, err)
				}
			} else if Status(err) != tc.wantStatus || Code(err) != "context_length_exceeded" || upstream != 0 {
				t.Fatalf("oversized input reached upstream or lost context code: upstream=%d err=%v", upstream, err)
			}
		})
	}
}

func TestDoubaoWorkLimitIncludesToolEnvelope(t *testing.T) {
	request := doubaoToolRequest()
	bridge, _ := newDoubaoTools(request)
	base, _ := bridge.prompt(arr(request["messages"]))
	text := str(obj(arr(request["messages"])[0])["content"])
	obj(arr(request["messages"])[0])["content"] = text + strings.Repeat("x", doubao.WorkMaxInputCharacters-utf8.RuneCountInString(base))
	r := testRouter(t, "doubao", func(w http.ResponseWriter, req *http.Request) {
		doubaoTestBlock(w, "STREAM_CHUNK", "answer", 10000, doubaoToolReply)
		sendEvent(w, "SSE_REPLY_END", map[string]any{"end_type": 3})
	}, Account{ID: "account"})
	body, _ := json.Marshal(request)
	if err := r.Chat(context.Background(), body, "", func(map[string]any) error { return nil }, nil); err != nil {
		t.Fatalf("tool envelope boundary rejected: %v", err)
	}
	obj(arr(request["messages"])[0])["content"] = str(obj(arr(request["messages"])[0])["content"]) + "x"
	body, _ = json.Marshal(request)
	if err := r.Chat(context.Background(), body, "", func(map[string]any) error { return nil }, nil); Code(err) != "context_length_exceeded" {
		t.Fatalf("tool prompt overhead ignored: %v", err)
	}
}
