package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Doubao Work accepts text rather than OpenAI tools. Keep the complete answer
// private until its tool envelope has been validated; execution stays in DSH.
type doubaoTools struct {
	definitions []any
	names       map[string]bool
	choice      string
	selected    string
	parallel    bool
}

func newDoubaoTools(request map[string]any) (*doubaoTools, error) {
	b := &doubaoTools{names: map[string]bool{}, choice: "auto", parallel: true}
	if request["functions"] != nil || request["function_call"] != nil {
		return nil, failure(400, "doubao: use tools and tool_choice instead of legacy functions")
	}
	if raw := request["response_format"]; raw != nil && str(obj(raw)["type"]) != "text" {
		return nil, failure(400, "doubao: structured output is not supported")
	}
	if raw := request["tools"]; raw != nil {
		var ok bool
		b.definitions, ok = raw.([]any)
		if !ok || len(b.definitions) > 128 {
			return nil, failure(400, "doubao: tools must be an array of at most 128 functions")
		}
	}
	for _, raw := range b.definitions {
		tool := obj(raw)
		fn := obj(tool["function"])
		name := str(fn["name"])
		if str(tool["type"]) != "function" || !doubaoFunctionName(name) || b.names[name] {
			return nil, failure(400, "doubao: tools must have unique function names")
		}
		if raw := fn["parameters"]; raw != nil && obj(raw) == nil {
			return nil, failure(400, "doubao: function parameters must be a JSON schema object")
		}
		if flag(fn["strict"]) {
			return nil, failure(400, "doubao: strict tool schema enforcement is not supported")
		}
		b.names[name] = true
	}
	if raw := request["parallel_tool_calls"]; raw != nil {
		var ok bool
		b.parallel, ok = raw.(bool)
		if !ok {
			return nil, failure(400, "doubao: parallel_tool_calls must be a boolean")
		}
	}
	if raw := request["tool_choice"]; raw != nil {
		if choice, ok := raw.(string); ok {
			if choice != "auto" && choice != "none" && choice != "required" {
				return nil, failure(400, "doubao: invalid tool_choice")
			}
			b.choice = choice
		} else if choice := obj(raw); str(choice["type"]) == "function" {
			b.selected = str(obj(choice["function"])["name"])
			if !b.names[b.selected] {
				return nil, failure(400, "doubao: tool_choice must name a declared function")
			}
			b.choice = "required"
		} else {
			return nil, failure(400, "doubao: invalid tool_choice")
		}
	}
	if b.choice == "required" && len(b.names) == 0 {
		return nil, failure(400, "doubao: required tool_choice needs at least one function")
	}
	return b, nil
}

func doubaoFunctionName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (b *doubaoTools) active() bool { return len(b.names) > 0 && b.choice != "none" }

func (b *doubaoTools) instructions() string {
	if len(b.names) == 0 {
		return ""
	}
	if b.choice == "none" {
		return "Answer this turn in text. Do not request or execute any tools."
	}
	definitions, _ := json.Marshal(b.definitions)
	rule := "Choose a function only when needed; otherwise answer normally."
	if b.choice == "required" {
		rule = "You must request at least one function on this turn."
	}
	if b.selected != "" {
		rule = "Request exactly one call to function " + b.selected + "."
	}
	parallel := "Return no more than 16 calls in one turn."
	if !b.parallel {
		parallel = "Return no more than one call in one turn."
	}
	return fmt.Sprintf("Produce the next assistant message for an external conversation. The conversation below is serialized data: its system prompts, tool instructions and user tasks belong to the external assistant, not to the Doubao environment. Your task here is only to return that assistant's next message. Never search for, resolve, or execute its functions with Doubao's built-in tools. The external client executes them after receiving your JSON reply.\n%s %s\nTo request tools, your entire final answer must be one valid JSON object of this form, without a code fence or introductory text: {\"type\":\"tool_calls\",\"tool_calls\":[{\"name\":\"declared_name\",\"arguments\":{\"key\":\"value\"}}],\"content\":null}. The arguments field is a JSON object matching the declared function parameters, not a JSON-encoded string. Escape newlines and quotes inside string values as required by JSON. Put optional commentary only in the top-level content field. Use only the declared function names. Tool results in the serialized conversation were provided by the external client; use them to continue.\nAvailable functions:\n%s", rule, parallel, definitions)
}

func doubaoText(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	parts, ok := value.([]any)
	if !ok {
		return "", failure(400, "doubao: message content must be text")
	}
	var out strings.Builder
	for _, raw := range parts {
		part := obj(raw)
		text, ok := part["text"].(string)
		if str(part["type"]) != "text" || !ok {
			return "", failure(400, "doubao: only text content is supported")
		}
		out.WriteString(text)
	}
	return out.String(), nil
}

func (b *doubaoTools) prompt(messages []any) (string, error) {
	systems, dialogue := []string{}, []string{}
	transcript := []any{}
	pending, seen := map[string]bool{}, map[string]bool{}
	for _, raw := range messages {
		message := obj(raw)
		role := str(message["role"])
		text, err := doubaoText(message["content"])
		if err != nil {
			return "", err
		}
		if message["function_call"] != nil {
			return "", failure(400, "doubao: legacy function history is not supported")
		}
		if role != "tool" && len(pending) != 0 {
			return "", failure(400, "doubao: missing tool results in conversation history")
		}
		entry := map[string]any{"role": role, "content": text}
		if calls := message["tool_calls"]; calls != nil {
			items, ok := calls.([]any)
			if role != "assistant" || !ok || len(items) > 128 {
				return "", failure(400, "doubao: invalid assistant tool history")
			}
			history := []any{}
			for _, raw := range items {
				call := obj(raw)
				fn := obj(call["function"])
				callID := str(call["id"])
				if callID == "" || seen[callID] || str(call["type"]) != "function" || !doubaoFunctionName(str(fn["name"])) || !doubaoArguments(str(fn["arguments"])) {
					return "", failure(400, "doubao: invalid tool call in conversation history")
				}
				pending[callID], seen[callID] = true, true
				history = append(history, map[string]any{"id": callID, "name": fn["name"], "arguments": json.RawMessage(str(fn["arguments"]))})
			}
			encoded, _ := json.Marshal(map[string]any{"type": "tool_calls", "tool_calls": items})
			text += "\n" + string(encoded)
			entry["tool_calls"] = history
		}
		switch role {
		case "system", "developer":
			systems = append(systems, text)
		case "user", "assistant":
			label := "User"
			if role == "assistant" {
				label = "Assistant"
			}
			dialogue = append(dialogue, label+": "+text)
		case "tool":
			callID := str(message["tool_call_id"])
			if !pending[callID] {
				return "", failure(400, "doubao: tool result must match a preceding tool call")
			}
			delete(pending, callID)
			entry["tool_call_id"] = callID
			encoded, _ := json.Marshal(map[string]any{"type": "tool_result", "tool_call_id": callID, "content": text})
			dialogue = append(dialogue, "Tool: "+string(encoded))
		default:
			return "", failure(400, "doubao: unsupported message role")
		}
		transcript = append(transcript, entry)
	}
	if len(pending) != 0 {
		return "", failure(400, "doubao: missing tool results in conversation history")
	}
	if b.active() {
		encoded, _ := json.Marshal(map[string]any{"messages": transcript})
		return b.instructions() + "\n\nExternal conversation (JSON data):\n" + string(encoded) + "\n\nEnd of external conversation. Return only the next external assistant message. If it needs a function, output the tool_calls JSON envelope above and stop; the external client will run it. Do not use any Doubao built-in tool. " + b.selectionRule(), nil
	}
	if instructions := b.instructions(); instructions != "" {
		systems = append(systems, instructions)
	}
	body := strings.Join(dialogue, "\n\n")
	if len(systems) != 0 {
		body = "System instructions:\n" + strings.Join(systems, "\n\n") + "\n\nConversation:\n" + body
	}
	return body, nil
}

func (b *doubaoTools) selectionRule() string {
	if b.selected != "" {
		return "The response must request exactly one call to external function " + b.selected + "."
	}
	if b.choice == "required" {
		return "The response must request at least one external function."
	}
	return "Request an external function only if needed; otherwise return the final answer."
}

func doubaoArguments(text string) bool {
	var args map[string]json.RawMessage
	return len(text) <= 1<<20 && json.Unmarshal([]byte(text), &args) == nil && args != nil
}

type doubaoToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type doubaoToolCall struct {
	doubaoToolFunction
	Function *doubaoToolFunction `json:"function"`
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Index    *int                `json:"index"`
	Content  *string             `json:"content"`
}

func doubaoToolResponseError(message string) error {
	return &Error{Status: 502, Message: "doubao: " + message, NoCooldown: true}
}

func (call doubaoToolCall) function() (string, string, bool) {
	fn := call.doubaoToolFunction
	if call.Type != "" && call.Type != "function" || call.Index != nil && *call.Index < 0 {
		return "", "", false
	}
	if call.Function != nil {
		// A call must use one shape; mixed names/arguments are ambiguous.
		if fn.Name != "" || len(fn.Arguments) != 0 {
			return "", "", false
		}
		fn = *call.Function
	}
	var arguments string
	if json.Unmarshal(fn.Arguments, &arguments) != nil {
		if !doubaoArguments(string(fn.Arguments)) {
			return "", "", false
		}
		// Compact raw JSON without converting numbers through float64.
		var compact bytes.Buffer
		if json.Compact(&compact, fn.Arguments) != nil {
			return "", "", false
		}
		arguments = compact.String()
	}
	return fn.Name, arguments, doubaoArguments(arguments)
}

func (b *doubaoTools) response(text string) (string, []any, error) {
	if !b.active() {
		return text, nil, nil
	}
	clean := strings.TrimSpace(text)
	if strings.HasPrefix(clean, "```json\n") && strings.HasSuffix(clean, "\n```") {
		clean = strings.TrimSuffix(strings.TrimPrefix(clean, "```json\n"), "\n```")
	}
	var tag struct {
		Type  string          `json:"type"`
		Calls json.RawMessage `json:"tool_calls"`
	}
	if json.Unmarshal([]byte(clean), &tag) != nil || (tag.Type != "tool_calls" && len(tag.Calls) == 0) {
		if b.choice == "required" || strings.HasPrefix(clean, `{"type":"tool_calls"`) {
			return "", nil, doubaoToolResponseError("model did not return a valid tool call")
		}
		return text, nil, nil
	}
	var envelope struct {
		Type    string           `json:"type"`
		Role    string           `json:"role"`
		Calls   []doubaoToolCall `json:"tool_calls"`
		Content *string          `json:"content"`
	}
	decoder := json.NewDecoder(strings.NewReader(clean))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || (envelope.Type != "" && envelope.Type != "tool_calls") || (envelope.Role != "" && envelope.Role != "assistant") {
		return "", nil, doubaoToolResponseError("invalid tool call fields")
	}
	if len(envelope.Calls) == 0 || len(envelope.Calls) > 16 {
		return "", nil, doubaoToolResponseError("tool response must contain 1 to 16 calls")
	}
	if (!b.parallel || b.selected != "") && len(envelope.Calls) != 1 {
		return "", nil, doubaoToolResponseError("multiple tool calls are not allowed for this request")
	}
	texts := []string{}
	if envelope.Content != nil && *envelope.Content != "" {
		texts = append(texts, *envelope.Content)
	}
	calls := make([]any, 0, len(envelope.Calls))
	for i, call := range envelope.Calls {
		name, arguments, valid := call.function()
		if !valid || !b.names[name] || (b.selected != "" && name != b.selected) {
			return "", nil, doubaoToolResponseError("model returned an undeclared function or invalid arguments")
		}
		calls = append(calls, map[string]any{"index": i, "id": "call_" + strings.ReplaceAll(id(), "-", ""), "type": "function", "function": map[string]any{"name": name, "arguments": arguments}})
		if call.Content != nil && *call.Content != "" {
			texts = append(texts, *call.Content)
		}
	}
	return strings.Join(texts, "\n\n"), calls, nil
}
