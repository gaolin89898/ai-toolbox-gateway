//go:build windows

package googlehttp

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func systemProxy(target *url.URL) (*url.URL, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil, nil
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return nil, nil
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return nil, nil
	}
	bypass, _, _ := key.GetStringValue("ProxyOverride")
	host := strings.ToLower(target.Hostname())
	for _, pattern := range strings.Split(bypass, ";") {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "<local>" && !strings.Contains(host, ".") {
			return nil, nil
		}
		if match, _ := path.Match(pattern, host); match {
			return nil, nil
		}
		if match, _ := path.Match(pattern, strings.ToLower(target.Host)); match {
			return nil, nil
		}
	}
	address, scheme := strings.TrimSpace(server), "http"
	if strings.Contains(address, "=") {
		address = ""
		var socks string
		for _, entry := range strings.Split(server, ";") {
			protocol, value, ok := strings.Cut(entry, "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(protocol)) {
			case target.Scheme:
				address = strings.TrimSpace(value)
			case "socks":
				socks = strings.TrimSpace(value)
			}
		}
		if address == "" {
			address, scheme = socks, "socks5"
		}
	}
	if address == "" {
		return nil, nil
	}
	if !strings.Contains(address, "://") {
		address = scheme + "://" + address
	}
	proxy, err := url.Parse(address)
	if err != nil || proxy.Hostname() == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5") || (proxy.Path != "" && proxy.Path != "/") || proxy.RawQuery != "" || proxy.Fragment != "" {
		return nil, fmt.Errorf("Windows 系统代理地址无效，请检查系统代理设置")
	}
	return proxy, nil
}
