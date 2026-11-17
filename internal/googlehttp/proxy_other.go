//go:build !windows

package googlehttp

import "net/url"

func systemProxy(*url.URL) (*url.URL, error) { return nil, nil }
