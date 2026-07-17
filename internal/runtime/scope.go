package runtime

import (
	"net"
	"strings"
)

// Scope 判定一个主机是否落在授权范围内。
type Scope struct {
	targetHost   string
	allowedHosts []string
	allowPrivate bool
}

// NewScope 以目标主机、附加允许主机与私网策略构造范围。
func NewScope(targetHost string, allowedHosts []string, allowPrivate bool) Scope {
	normalized := make([]string, 0, len(allowedHosts))
	for _, host := range allowedHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			normalized = append(normalized, host)
		}
	}
	return Scope{
		targetHost:   strings.ToLower(strings.TrimSpace(targetHost)),
		allowedHosts: normalized,
		allowPrivate: allowPrivate,
	}
}

// HostAllowed 判定给定主机是否可访问。空主机（相对 URL）视为同源放行。
func (s Scope) HostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return true
	}
	if isLoopbackOrPrivate(host) {
		return s.allowPrivate
	}
	if matchHost(host, s.targetHost) {
		return true
	}
	for _, allowed := range s.allowedHosts {
		if matchHost(host, allowed) {
			return true
		}
	}
	return false
}

// matchHost 判断 host 是否等于 base 或为其子域。
func matchHost(host, base string) bool {
	if base == "" {
		return false
	}
	return host == base || strings.HasSuffix(host, "."+base)
}

func isLoopbackOrPrivate(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}
