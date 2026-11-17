//go:build windows

package doubaowork

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ClientSession 是本机豆包客户端的登录态：Cookie 串 + 设备上下文。
type ClientSession struct {
	AccountUID  string
	Cookie      string
	DeviceID    string
	WebID       string
	Fingerprint string
}

// ImportClientSession 读取本机豆包客户端的登录态：从 Chromium 配置档解出
// doubao.com Cookie（DPAPI + AES-GCM），并从 SDK 存储取设备号。
func ImportClientSession() (ClientSession, error) {
	userData := filepath.Join(os.Getenv("LOCALAPPDATA"), "Doubao", "User Data")
	if st, err := os.Stat(userData); err != nil || !st.IsDir() {
		return ClientSession{}, fmt.Errorf("未找到豆包客户端数据目录，请先安装并登录豆包客户端")
	}
	statePath := filepath.Join(userData, "Local State")
	state, err := os.ReadFile(statePath)
	if err != nil {
		return ClientSession{}, fmt.Errorf("豆包客户端当前账号配置读取失败")
	}
	profile, uid, err := activeClientProfile(state)
	if err != nil {
		return ClientSession{}, err
	}
	key, err := localStateKey(statePath)
	if err != nil {
		return ClientSession{}, fmt.Errorf("豆包客户端密钥读取失败：%w", err)
	}
	cookies, err := readProfileCookies(filepath.Join(userData, profile, "Network", "Cookies"), key)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ClientSession{}, fmt.Errorf("豆包客户端正在占用当前账号登录态，请完全退出豆包客户端后重试导入（无需退出账号）")
	}
	if err != nil || cookies["sessionid"] == "" {
		return ClientSession{}, fmt.Errorf("豆包客户端当前账号登录态读取失败，请在当前账号重新登录后重试")
	}
	// Import only the selected profile. Other profiles may retain valid sessions
	// for different accounts; they must never be used as a fallback.
	current, err := os.ReadFile(statePath)
	if err != nil {
		return ClientSession{}, fmt.Errorf("豆包客户端当前账号配置读取失败")
	}
	currentProfile, currentUID, err := activeClientProfile(current)
	if err != nil || currentProfile != profile || currentUID != uid {
		return ClientSession{}, fmt.Errorf("豆包客户端账号已切换，请重新导入")
	}
	session := ClientSession{AccountUID: uid, Cookie: cookieHeader(cookies), Fingerprint: cookies["s_v_web_id"]}
	session.DeviceID, session.WebID = deviceContext(userData)
	if session.DeviceID == "" {
		return ClientSession{}, fmt.Errorf("未找到豆包客户端设备号，请在客户端完成一次对话后重试")
	}
	return session, nil
}

// activeClientProfile follows the client's last-used profile and preserves its
// expected identity so the authenticated response can be checked before saving.
func activeClientProfile(raw []byte) (string, string, error) {
	var state struct {
		Profile struct {
			LastUsed  string `json:"last_used"`
			InfoCache map[string]struct {
				Saman struct {
					UserID string `json:"user_id"`
				} `json:"saman"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return "", "", fmt.Errorf("豆包客户端当前账号配置无效")
	}
	name := state.Profile.LastUsed
	valid := name == "Default"
	if strings.HasPrefix(name, "Profile ") {
		suffix := strings.TrimPrefix(name, "Profile ")
		valid = suffix != "" && strings.Trim(suffix, "0123456789") == ""
	}
	info, exists := state.Profile.InfoCache[name]
	uid := info.Saman.UserID
	if !valid || !exists || uid == "" || strings.Trim(uid, "0") == "" || strings.Trim(uid, "0123456789") != "" {
		return "", "", fmt.Errorf("无法确认豆包客户端当前账号，请在客户端登录并切换到要导入的账号后重试")
	}
	return name, uid, nil
}

// deviceContext 从 SDK 存储提取 device_id / web_id（标记后紧跟数字）。
func deviceContext(userData string) (string, string) {
	raw, err := os.ReadFile(filepath.Join(userData, "sdk_storage", "common.db"))
	if err != nil {
		return "", ""
	}
	text := string(raw)
	return digitsAfter(text, "device_id"), digitsAfter(text, "web_id")
}

// digitsAfter 找到标记后紧邻的数字串（跳过引号、冒号、等号、空格）。
func digitsAfter(text, marker string) string {
	idx := strings.Index(text, marker)
	for idx >= 0 {
		i := idx + len(marker)
		for i < len(text) {
			c := text[i]
			if c >= '1' && c <= '9' {
				break
			}
			if c != '"' && c != ':' && c != '=' && c != ' ' {
				i = -1
				break
			}
			i++
		}
		if i >= 0 {
			j := i
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				j++
			}
			if j-i >= 12 && j-i <= 20 {
				return text[i:j]
			}
		}
		next := strings.Index(text[idx+len(marker):], marker)
		if next < 0 {
			break
		}
		idx = idx + len(marker) + next
	}
	return ""
}

func cookieHeader(cookies map[string]string) string {
	names := make([]string, 0, len(cookies))
	for name := range cookies {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+cookies[name])
	}
	return strings.Join(parts, "; ")
}

// readProfileCookies 读取 doubao.com 的 Cookie（值加密时用 DPAPI 解出的 AES 密钥解密）。
func readProfileCookies(path string, key []byte) (map[string]string, error) {
	rows, err := sqliteRows(path, "cookies")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		host, _ := row[1].(string)
		if !strings.Contains(host, "doubao.com") {
			continue
		}
		name, _ := row[3].(string)
		value, _ := row[4].(string)
		enc, _ := row[5].([]byte)
		if name == "" {
			continue
		}
		if value == "" && len(enc) > 0 {
			if plain, err := decryptCookie(key, enc); err == nil {
				value = plain
			}
		}
		if value != "" {
			out[name] = value
		}
	}
	return out, nil
}

// --- DPAPI / AES-GCM（Chromium v10 方案）---

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	crypt32           = windows.NewLazySystemDLL("crypt32.dll")
	procUnprotectData = crypt32.NewProc("CryptUnprotectData")
)

func dpapiUnprotect(data []byte) ([]byte, error) {
	var in, out dataBlob
	in.cbData = uint32(len(data))
	if len(data) > 0 {
		in.pbData = &data[0]
	}
	r, _, err := procUnprotectData.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(out.pbData)))
	res := make([]byte, out.cbData)
	copy(res, unsafe.Slice(out.pbData, out.cbData))
	return res, nil
}

// localStateKey 从 Local State 解出 Chromium Cookie 的 AES-256 密钥。
func localStateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ls struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(raw, &ls); err != nil {
		return nil, err
	}
	if ls.OSCrypt.EncryptedKey == "" {
		return nil, errors.New("缺少 os_crypt.encrypted_key")
	}
	key, err := base64.StdEncoding.DecodeString(ls.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, err
	}
	if len(key) < 6 || string(key[:5]) != "DPAPI" {
		return nil, errors.New("未知的密钥格式")
	}
	return dpapiUnprotect(key[5:])
}

func decryptCookie(key, blob []byte) (string, error) {
	if len(blob) < 3+12+16 {
		return "", errors.New("密文过短")
	}
	prefix := string(blob[:3])
	if prefix != "v10" && prefix != "v11" {
		return "", fmt.Errorf("不支持的密文前缀 %q", prefix)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, blob[3:15], blob[15:], nil)
	if err != nil {
		return "", err
	}
	// Chromium 在明文前附 32 字节 SHA256(host_key)。
	if len(plain) > 32 {
		rest := plain[32:]
		printable := true
		for _, c := range rest {
			if c < 0x20 || c > 0x7e {
				printable = false
				break
			}
		}
		if printable {
			return string(rest), nil
		}
	}
	return string(plain), nil
}

// --- 最小 SQLite 读取（仅表叶子行）---

func sqliteRows(path, table string) ([][]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 100 || string(raw[:15]) != "SQLite format 3" || raw[15] != 0 {
		return nil, errors.New("不是有效的 SQLite 文件")
	}
	pageSize := int(binary.BigEndian.Uint16(raw[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	roots := findTableRoots(raw, pageSize, table)
	if len(roots) == 0 {
		return nil, fmt.Errorf("未找到表 %s", table)
	}
	var lastErr error
	for _, root := range roots {
		rows, err := treeRows(raw, pageSize, root)
		if err == nil {
			return rows, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func treeRows(raw []byte, pageSize, root int) ([][]any, error) {
	var rows [][]any
	var walk func(page int) error
	walk = func(page int) error {
		base := (page - 1) * pageSize
		if base < 0 || base+12 > len(raw) {
			return errors.New("页越界")
		}
		off := base
		if page == 1 {
			off = base + 100
		}
		typ := raw[off]
		count := int(binary.BigEndian.Uint16(raw[off+3:]))
		ptr := off + 8
		if typ == 0x05 {
			ptr = off + 12
		}
		if ptr+count*2 > len(raw) {
			return nil
		}
		switch typ {
		case 0x0d:
			for i := 0; i < count; i++ {
				cell := base + int(binary.BigEndian.Uint16(raw[ptr+2*i:]))
				plen, n1 := sqliteVarint(raw, cell)
				_, n2 := sqliteVarint(raw, cell+n1)
				start := cell + n1 + n2
				if start < 0 || start+int(plen) > len(raw) {
					continue
				}
				if vals, err := sqliteRecord(raw[start : start+int(plen)]); err == nil {
					rows = append(rows, vals)
				}
			}
		case 0x05:
			for i := 0; i < count; i++ {
				cell := base + int(binary.BigEndian.Uint16(raw[ptr+2*i:]))
				if cell+4 > len(raw) {
					continue
				}
				child := int(binary.BigEndian.Uint32(raw[cell:]))
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return rows, walk(root)
}

// findTableRoot 扫描所有叶子页，找到 sqlite_master 中目标表的根页。
// 不依赖 sqlite_master 的树深度（大于一页时可能是内部页）。
func findTableRoots(raw []byte, pageSize int, table string) []int {
	roots := []int{}
	for page := 1; ; page++ {
		base := (page - 1) * pageSize
		if base+pageSize > len(raw) {
			break
		}
		off := base
		if page == 1 {
			off = base + 100
		}
		if raw[off] != 0x0d {
			continue
		}
		count := int(binary.BigEndian.Uint16(raw[off+3:]))
		if off+8+count*2 > len(raw) {
			continue
		}
		for i := 0; i < count; i++ {
			cell := base + int(binary.BigEndian.Uint16(raw[off+8+2*i:]))
			plen, n1 := sqliteVarint(raw, cell)
			_, n2 := sqliteVarint(raw, cell+n1)
			start := cell + n1 + n2
			if plen <= 0 || start < 0 || start+int(plen) > len(raw) {
				continue
			}
			vals, err := sqliteRecord(raw[start : start+int(plen)])
			if err != nil || len(vals) < 4 {
				continue
			}
			if name, _ := vals[1].(string); name == table {
				if v, ok := vals[3].(int64); ok {
					roots = append(roots, int(v))
				}
			}
		}
	}
	return roots
}

func sqliteVarint(b []byte, at int) (int64, int) {
	var v int64
	for i := 0; i < 9; i++ {
		if at+i >= len(b) {
			return v, i
		}
		c := b[at+i]
		if i == 8 {
			return (v << 8) | int64(c), 9
		}
		v = (v << 7) | int64(c&0x7f)
		if c&0x80 == 0 {
			return v, i + 1
		}
	}
	return v, 9
}

func sqliteRecord(payload []byte) ([]any, error) {
	hdrLen, n := sqliteVarint(payload, 0)
	pos := n
	hdrEnd := int(hdrLen)
	if hdrEnd < pos || hdrEnd > len(payload) {
		return nil, errors.New("记录头越界")
	}
	types := []int64{}
	for pos < hdrEnd {
		t, m := sqliteVarint(payload, pos)
		types = append(types, t)
		pos += m
	}
	vals := make([]any, 0, len(types))
	off := hdrEnd
	sizes := map[int64]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 6, 6: 8, 7: 8}
	for _, t := range types {
		if t >= 12 {
			size := int((t - 12) / 2)
			if off+size > len(payload) {
				return nil, errors.New("记录体越界")
			}
			chunk := payload[off : off+size]
			off += size
			if t%2 == 1 {
				vals = append(vals, string(chunk))
			} else {
				vals = append(vals, append([]byte{}, chunk...))
			}
			continue
		}
		if t == 0 {
			vals = append(vals, nil)
			continue
		}
		if t == 8 || t == 9 {
			var v int64
			if t == 9 {
				v = 1
			}
			vals = append(vals, v)
			continue
		}
		size, ok := sizes[t]
		if !ok || off+size > len(payload) {
			return nil, errors.New("记录字段越界")
		}
		var v int64
		for i := 0; i < size; i++ {
			v = (v << 8) | int64(payload[off+i])
		}
		shift := uint(64 - 8*size)
		v = v << shift >> shift
		off += size
		vals = append(vals, v)
	}
	return vals, nil
}
