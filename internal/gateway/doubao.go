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

	doubao "github.com/linguo2625469/workbuddy2api-panel/internal/doubaowork"
)

// doubaoWorkModels 把豆包工作的官方模型目录（model_item_key）映射为网关模型。
func doubaoWorkModels() []Model {
	out := []Model{}
	for _, m := range doubao.WorkCatalog {
		f := map[string]any{"name": m.Name, "catalog_source": "work_builtin"}
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
	for _, key := range []string{"tools", "functions", "tool_choice", "function_call", "response_format"} {
		if v := request[key]; v != nil {
			return failure(400, "doubao: tools and structured output are not supported")
		}
	}
	texts := []string{}
	for _, v := range arr(request["messages"]) {
		message := obj(v)
		role := str(message["role"])
		switch role {
		case "system", "developer", "user", "assistant":
		default:
			return failure(400, "doubao: unsupported message role")
		}
		if len(arr(message["tool_calls"])) > 0 || message["function_call"] != nil {
			return failure(400, "doubao: tool messages are not supported")
		}
		text, ok := message["content"].(string)
		if !ok {
			parts, valid := message["content"].([]any)
			if !valid {
				return failure(400, "doubao: message content must be text")
			}
			for _, p := range parts {
				part := obj(p)
				value, valid := part["text"].(string)
				if str(part["type"]) != "text" || !valid {
					return failure(400, "doubao: only text content is supported")
				}
				text += value
			}
		}
		texts = append(texts, role+": "+text)
	}
	prompt := strings.Join(texts, string(rune(10))+string(rune(10)))
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
	previous := map[string]string{}
	reasoningIDs := map[string]bool{}
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
			break
		}
		var frame map[string]any
		if json.Unmarshal([]byte(rawFrame), &frame) != nil {
			continue
		}
		if code := num(frame["error_code"]); code != 0 {
			return failure(502, "doubao: upstream error "+strconv.Itoa(int(code)))
		}
		blocks := []map[string]any{}
		collectWorkBlocks(frame, 0, false, 0, &blocks)
		appendMode := event == "STREAM_CHUNK" || event == "CHUNK_DELTA"
		for _, block := range blocks {
			blockType := int(num(block["block_type"]))
			blockID := str(block["block_id"])
			if blockType == 10040 && blockID != "" {
				reasoningIDs[blockID] = true
			}
			tb := obj(obj(block["content"])["text_block"])
			text := str(tb["text"])
			if text == "" {
				continue
			}
			key := blockID
			if key == "" {
				key = "b" + strconv.Itoa(blockType)
			}
			before := previous[key]
			var delta string
			if appendMode {
				previous[key] = before + text
				delta = text
			} else if strings.HasPrefix(text, before) {
				previous[key] = text
				delta = text[len(before):]
			} else {
				previous[key] = text
				delta = text
			}
			if delta == "" {
				continue
			}
			if !started {
				if err := chunk(map[string]any{"role": "assistant"}, nil); err != nil {
					return err
				}
				started = true
			}
			field := "content"
			if blockType == 10040 || reasoningIDs[key] || strings.Contains(strings.ToLower(str(tb["icon_url"])), "deep_think") {
				field = "reasoning_content"
			}
			if err := chunk(map[string]any{field: delta}, nil); err != nil {
				return err
			}
		}
		if event == "SSE_REPLY_END" {
			if int(num(frame["end_type"])) == 3 {
				break
			}
			event = ""
			continue
		}
		event = ""
	}
	if !started {
		return failure(502, "doubao: upstream returned no text")
	}
	return chunk(map[string]any{}, "stop")
}

// collectWorkBlocks 递归收集带 block_type 与 content 的块对象。
// user_type=1 的子树是用户消息回显（FULL_MSG_NOTIFY），整棵跳过。
func collectWorkBlocks(v any, userType int, hasContext bool, depth int, out *[]map[string]any) {
	if depth > 10 || v == nil {
		return
	}
	switch t := v.(type) {
	case []any:
		for _, c := range t {
			collectWorkBlocks(c, userType, hasContext, depth+1, out)
		}
	case map[string]any:
		if raw, ok := t["user_type"]; ok {
			userType = int(num(raw))
			hasContext = true
		}
		if hasContext && userType == 1 {
			return
		}
		if t["block_type"] != nil && t["content"] != nil {
			*out = append(*out, t)
		}
		for _, c := range t {
			collectWorkBlocks(c, userType, hasContext, depth+1, out)
		}
	}
}

// commonSuffixAppend 在上游改写快照时取新增尾部，避免重复输出。
func commonSuffixAppend(before, now string) string {
	n := 0
	for n < len(before) && n < len(now) && before[n] == now[n] {
		n++
	}
	if n >= len(now) {
		return ""
	}
	return now[n:]
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
