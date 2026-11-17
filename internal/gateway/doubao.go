package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	doubao "github.com/linguo2625469/workbuddy2api-panel/internal/doubaowork"
)

// doubaoWorkModels 把豆包工作的官方模型目录（model_item_key）映射为网关模型。
func doubaoWorkModels() []Model {
	out := []Model{}
	for _, m := range doubao.WorkCatalog {
		f := map[string]any{"name": m.Name, "catalog_source": "work_builtin", "supports_tool_call": true, "tool_call_mode": "text_bridge", "supports_images": false, "max_input_characters": doubao.WorkMaxInputCharacters}
		if m.ContextWindow > 0 {
			f["context_length"] = m.ContextWindow
		}
		if len(m.Efforts) > 1 {
			f["supports_reasoning"] = true
			f["supported_efforts"] = m.Efforts
			f["default_effort"] = m.DefaultEffort
		}
		out = append(out, Model{ID: m.ID, Fields: f})
	}
	return out
}

// doubaoChat 走豆包工作 /chat/completion 协议（客户端会话，model_item_key 选模型）。
func (r *Router) doubaoChat(ctx context.Context, c Credential, m Model, request map[string]any, emit Emit) error {
	bridge, err := newDoubaoTools(request)
	if err != nil {
		return err
	}
	prompt, err := bridge.prompt(arr(request["messages"]))
	if err != nil {
		return err
	}
	if characters := utf8.RuneCountInString(prompt); characters > doubao.WorkMaxInputCharacters {
		return &Error{Status: 400, Code: "context_length_exceeded", Message: "doubao: maximum context length for a Work text input is " + strconv.Itoa(doubao.WorkMaxInputCharacters) + " characters; this request has " + strconv.Itoa(characters) + ". Compact the conversation before retrying."}
	}
	effort := doubao.WorkEffort(m.ID, str(request["reasoning_effort"]))
	payload := doubao.WorkPayload(prompt, m.ID, effort, c.DeviceID)
	raw, err := json.Marshal(payload)
	if err != nil {
		return failure(400, "invalid request parameters")
	}
	url := r.Bases["doubao"] + "/chat/completion?" + doubao.WorkQuery(c.DeviceID, c.WebID, c.Fingerprint)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(raw))
	if err != nil {
		return failure(502, "doubao: invalid endpoint")
	}
	req.Header = doubaoWorkHeaders(c)
	resp, err := r.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure(502, "doubao: upstream connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return failure(502, "doubao: upstream rejected request (HTTP "+strconv.Itoa(resp.StatusCode)+")")
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return failure(502, "doubao: expected an event stream; open the Doubao client and log in again")
	}
	completionID, created := "chatcmpl-"+id(), time.Now().Unix()
	started := false
	chunk := func(delta map[string]any, finish any) error {
		return emit(map[string]any{"id": completionID, "object": "chat.completion.chunk", "created": created, "model": m.ID,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	}
	parser := newDoubaoTextParser()
	var answer strings.Builder
	ended := false
	event, data := "", []string{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(line[6:])
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimLeft(line[5:], " "))
			continue
		}
		if line != "" || len(data) == 0 {
			continue
		}
		rawFrame := strings.Join(data, string(rune(10)))
		data = nil
		if rawFrame == "[DONE]" {
			ended = true
			break
		}
		var frame map[string]any
		if json.Unmarshal([]byte(rawFrame), &frame) != nil {
			continue
		}
		if code := num(frame["error_code"]); code != 0 {
			if code == 710020202 {
				return failure(400, "doubao: upstream rejected request parameters (710020202)")
			}
			if code == -1 && str(frame["error_msg"]) == "delegated input protocol violation" {
				return failure(502, "doubao: upstream rejected the conversation format (delegated input protocol violation)")
			}
			return failure(502, "doubao: upstream error "+strconv.Itoa(int(code)))
		}
		fragments, err := parser.feed(event, frame)
		if err != nil {
			return err
		}
		for _, fragment := range fragments {
			field, delta := fragment.field, fragment.text
			if delta == "" {
				continue
			}
			if !started {
				if err := chunk(map[string]any{"role": "assistant"}, nil); err != nil {
					return err
				}
				started = true
			}
			if bridge.active() && field == "content" {
				if answer.Len()+len(delta) > 8<<20 {
					return failure(502, "doubao: tool response exceeds the size limit")
				}
				answer.WriteString(delta)
				continue
			}
			if err := chunk(map[string]any{field: delta}, nil); err != nil {
				return err
			}
		}
		if event == "SSE_REPLY_END" {
			if int(num(frame["end_type"])) == 3 {
				ended = true
				break
			}
			event = ""
			continue
		}
		event = ""
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure(502, "doubao: upstream stream interrupted")
	}
	if !started {
		return failure(502, "doubao: upstream returned no text")
	}
	if bridge.active() {
		if !ended {
			return failure(502, "doubao: upstream closed before completing the tool response")
		}
		if answer.Len() == 0 {
			return failure(502, "doubao: upstream returned no answer for the tool request")
		}
		content, calls, err := bridge.response(answer.String())
		if err != nil {
			return err
		}
		if content != "" {
			if err := chunk(map[string]any{"content": content}, nil); err != nil {
				return err
			}
		}
		if len(calls) > 0 {
			if err := chunk(map[string]any{"tool_calls": calls}, nil); err != nil {
				return err
			}
			return chunk(map[string]any{}, "tool_calls")
		}
	}
	return chunk(map[string]any{}, "stop")
}

// doubaoWorkHeaders 构造豆包工作请求头（与客户端一致）。
func doubaoWorkHeaders(c Credential) http.Header {
	h := http.Header{}
	h.Set("Accept", "text/event-stream")
	h.Set("Content-Type", "application/json")
	h.Set("Cookie", c.Token)
	h.Set("Origin", "https://www.doubao.com")
	h.Set("Referer", "https://www.doubao.com/chat/")
	h.Set("User-Agent", doubao.WorkUA)
	h.Set("agw-js-conv", "str")
	h.Set("last-event-id", "undefined")
	h.Set("x-lgw-req-sdk-type", "3")
	h.Set("x-net-sdk-domain-dispatch", "1")
	h.Set("x-request-id", id())
	h.Set("x-ttnet-bypass-decompression", "1")
	h.Set("x-flow-trace", "04-"+strings.ReplaceAll(id(), "-", ""))
	if csrf := doubaoCSRF(c.Token); csrf != "" {
		h.Set("x-tt-passport-csrf-token", csrf)
	}
	return h
}

// doubaoCSRF 从 Cookie 串中取出 passport_csrf_token。
func doubaoCSRF(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "passport_csrf_token=") {
			return strings.TrimPrefix(part, "passport_csrf_token=")
		}
	}
	return ""
}
