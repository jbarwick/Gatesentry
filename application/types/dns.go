package GatesentryTypes

// DNSCustomEntry is a manually configured name. IPv4, IPv6, or both may be set;
// at least one address is required.
type DNSCustomEntry struct {
	Domain string `json:"domain"`
	IP     string `json:"ip,omitempty"`
	IPv6   string `json:"ipv6,omitempty"`
}

type DnsServerInfo struct {
	NumberDomainsBlocked int `json:"number_domains_blocked"`
	LastUpdated          int `json:"last_updated"`
	NextUpdate           int `json:"next_update"`
}
