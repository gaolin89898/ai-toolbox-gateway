package doubaowork

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
)

// newID 返回 UUID v4 文本形式（豆包工作请求里的 local_message_id 等字段）。
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(i * 37)
		}
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func idNoDash() string { return strings.ReplaceAll(newID(), "-", "") }

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }
