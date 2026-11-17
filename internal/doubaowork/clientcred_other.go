//go:build !windows

package doubaowork

import "errors"

// ClientSession 仅 Windows 客户端支持。
type ClientSession struct {
	AccountUID  string
	Cookie      string
	DeviceID    string
	WebID       string
	Fingerprint string
}

// ImportClientSession 非 Windows 平台没有豆包客户端本地存储。
func ImportClientSession() (ClientSession, error) {
	return ClientSession{}, errors.New("仅支持从 Windows 豆包客户端导入登录态")
}
