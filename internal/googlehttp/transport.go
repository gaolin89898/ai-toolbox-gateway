// Package googlehttp applies the desktop's proxy settings to Google requests.
package googlehttp

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

func NewTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxyForRequest
	return transport
}

func proxyForRequest(req *http.Request) (*url.URL, error) {
	proxy, err := http.ProxyFromEnvironment(req)
	if err != nil || proxy != nil {
		return proxy, err
	}
	// Explicit environment settings, including NO_PROXY, take precedence.
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		if os.Getenv(name) != "" {
			return nil, nil
		}
	}
	host := strings.ToLower(req.URL.Hostname())
	if req.URL.Scheme != "https" || !(host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com") || host == "accounts.google.com") {
		return nil, nil
	}
	return systemProxy(req.URL)
}
