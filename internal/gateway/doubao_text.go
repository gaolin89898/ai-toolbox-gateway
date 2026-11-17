package gateway

import (
	"encoding/json"
	"strings"
)

type doubaoTextFragment struct{ field, text string }
type doubaoTextBlock struct {
	value   map[string]any
	message string
	append  bool
}

// Block patches and CHUNK_DELTA carry consecutive pieces of the same text.
// Snapshots repeat that text; only their previously unseen suffix is emitted.
type doubaoTextParser struct {
	message, active string
	text            map[string]string
	kinds           map[string]string
	reasoning       map[string]bool
}

func newDoubaoTextParser() *doubaoTextParser {
	return &doubaoTextParser{message: "default", text: map[string]string{}, kinds: map[string]string{}, reasoning: map[string]bool{}}
}

func (p *doubaoTextParser) key(message, block string) string {
	if block == "" {
		block = "default"
	}
	return message + "\x00" + block
}

func (p *doubaoTextParser) selectMessage(message string) {
	if message != "" && message != p.message {
		p.message, p.active = message, ""
	}
}

func (p *doubaoTextParser) add(key, field, text string, appendMode bool) (doubaoTextFragment, error) {
	fragment := doubaoTextFragment{field: field}
	if text == "" {
		return fragment, nil
	}
	stateKey := key + "\x00" + field
	before := p.text[stateKey]
	if appendMode {
		p.text[stateKey] = before + text
		fragment.text = text
	} else if strings.HasPrefix(text, before) {
		p.text[stateKey] = text
		fragment.text = text[len(before):]
	} else if !strings.HasPrefix(before, text) {
		// Already emitted text cannot be replaced safely, especially in tool JSON.
		return fragment, failure(502, "doubao: upstream revised previously streamed text; retry the request")
	}
	return fragment, nil
}

func (p *doubaoTextParser) feed(event string, frame map[string]any) ([]doubaoTextFragment, error) {
	if num(frame["user_type"]) == 1 || num(obj(frame["message"])["user_type"]) == 1 {
		return nil, nil
	}
	message := str(frame["message_id"])
	if message == "" {
		message = str(obj(frame["meta"])["message_id"])
	}
	p.selectMessage(message)
	if event == "CHUNK_DELTA" {
		if text, ok := frame["text"].(string); ok {
			key := p.active
			if block := str(frame["block_id"]); block != "" || key == "" {
				key = p.key(p.message, block)
			}
			field := p.kinds[key]
			if field == "" {
				field = "content"
			}
			if thinking, ok := frame["_thinking"].(bool); ok {
				field = "content"
				if thinking {
					field = "reasoning_content"
				}
			}
			p.active, p.kinds[key] = key, field
			fragment, err := p.add(key, field, text, true)
			return []doubaoTextFragment{fragment}, err
		}
	}
	var blocks []doubaoTextBlock
	if err := collectDoubaoTextBlocks(frame, p.message, event == "STREAM_CHUNK", event == "FULL_MSG_NOTIFY", 0, &blocks); err != nil {
		return nil, err
	}
	for _, block := range blocks {
		if num(block.value["block_type"]) == 10040 {
			p.reasoning[p.key(block.message, str(block.value["block_id"]))] = true
		}
	}
	var fragments []doubaoTextFragment
	for _, item := range blocks {
		block := item.value
		kind := num(block["block_type"])
		if kind != 10000 && kind != 10040 {
			continue
		}
		tb := obj(obj(block["content"])["text_block"])
		if tb == nil || (kind == 10040 && str(tb["text"]) == "") {
			continue
		}
		p.selectMessage(item.message)
		key := p.key(p.message, str(block["block_id"]))
		parent := str(block["parent_id"])
		if parent == "" {
			parent = str(block["parent_block_id"])
		}
		if parent == "" {
			parent = str(obj(block["control_info"])["parent_block_id"])
		}
		field := p.kinds[key]
		if field == "" {
			field = "content"
		}
		if kind == 10040 || p.reasoning[key] || (parent != "" && p.reasoning[p.key(p.message, parent)]) || strings.Contains(strings.ToLower(str(tb["icon_url"])+str(tb["icon_url_dark"])), "deep_think") {
			field = "reasoning_content"
		}
		// Empty text blocks still establish where subsequent bare deltas belong.
		p.active, p.kinds[key] = key, field
		fragment, err := p.add(key, field, str(tb["text"]), item.append)
		if err != nil {
			return nil, err
		}
		if fragment.text != "" {
			fragments = append(fragments, fragment)
		}
	}
	return fragments, nil
}

// Traverse protocol containers in their wire order. User message echoes are
// excluded, and patch_type overrides the enclosing event's default text mode.
func collectDoubaoTextBlocks(v any, message string, appendMode, decodeContent bool, depth int, blocks *[]doubaoTextBlock) error {
	if depth > 12 {
		return nil
	}
	switch value := v.(type) {
	case []any:
		for _, child := range value {
			if err := collectDoubaoTextBlocks(child, message, appendMode, decodeContent, depth+1, blocks); err != nil {
				return err
			}
		}
	case map[string]any:
		if num(value["user_type"]) == 1 {
			return nil
		}
		if identity := str(value["message_id"]); identity != "" {
			message = identity
		}
		if patch, ok := value["patch_type"]; ok {
			appendMode = num(patch) == 1 || str(patch) == "1"
		}
		if value["block_type"] != nil && value["content"] != nil {
			*blocks = append(*blocks, doubaoTextBlock{value: value, message: message, append: appendMode})
			return nil
		}
		for _, name := range []string{"message", "content", "content_block", "patch_op", "patch_value"} {
			child := value[name]
			if text, ok := child.(string); ok && name == "content" && decodeContent {
				if json.Unmarshal([]byte(text), &child) != nil {
					return failure(502, "doubao: invalid full message snapshot")
				}
			}
			if err := collectDoubaoTextBlocks(child, message, appendMode, decodeContent, depth+1, blocks); err != nil {
				return err
			}
		}
	}
	return nil
}
