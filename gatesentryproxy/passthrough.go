package gatesentryproxy

import (
	"net"
	"net/http"
	"strings"
)

// PassthroughManagementHost reports destinations that GateSentry itself must
// reach even if rules, time blocks, or DNS lists would otherwise deny them.
// These are public API hostnames, not site LAN addresses.
func PassthroughManagementHost(host string) bool {
	host = strings.ToLower(strings.TrimRight(strings.TrimSpace(host), "."))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = strings.ToLower(h)
	}
	switch host {
	case "api.x.ai", "api.openai.com":
		return true
	}
	return false
}

func requestHostname(r *http.Request) string {
	h := ""
	if r != nil {
		h = r.URL.Host
		if h == "" {
			h = r.Host
		}
	}
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		host = h
	}
	return strings.ToLower(strings.TrimRight(host, "."))
}
