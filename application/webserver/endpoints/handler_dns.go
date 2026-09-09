package gatesentryWebserverEndpoints

import (
	"encoding/json"
	"errors"
	"net"
	"strings"

	gatesentry2storage "bitbucket.org/abdullah_irfan/gatesentryf/storage"
	gatesentryTypes "bitbucket.org/abdullah_irfan/gatesentryf/types"
	gatesentryWebserverTypes "bitbucket.org/abdullah_irfan/gatesentryf/webserver/types"
)

func GSApiDNSInfo(dnsServerInfo *gatesentryTypes.DnsServerInfo) interface{} {

	return dnsServerInfo
}

func GSApiDNSEntriesCustom(data string, settings *gatesentry2storage.MapStore, runtime *gatesentryWebserverTypes.TemporaryRuntime) interface{} {

	var customEntries []gatesentryTypes.DNSCustomEntry
	json.Unmarshal([]byte(data), &customEntries)
	if customEntries == nil {
		customEntries = []gatesentryTypes.DNSCustomEntry{}
	}

	return struct {
		Data []gatesentryTypes.DNSCustomEntry `json:"data"`
	}{Data: customEntries}
}

func GSApiDNSSaveEntriesCustom(customEntries []gatesentryTypes.DNSCustomEntry, settings *gatesentry2storage.MapStore, runtime *gatesentryWebserverTypes.TemporaryRuntime) interface{} {
	if err := normalizeCustomDNSEntries(customEntries); err != nil {
		return struct {
			Error string `json:"error"`
		}{Error: err.Error()}
	}

	jsonData, err := json.Marshal(customEntries)
	if err != nil {
		return struct {
			Error string `json:"error"`
		}{Error: err.Error()}
	}

	settings.Update("DNS_custom_entries", string(jsonData))

	return struct {
		Ok bool `json:"ok"`
	}{Ok: true}

}

func normalizeCustomDNSEntries(entries []gatesentryTypes.DNSCustomEntry) error {
	seen := make(map[string]bool, len(entries))
	for i := range entries {
		e := &entries[i]
		e.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(e.Domain), "."))
		e.IP = strings.TrimSpace(e.IP)
		e.IPv6 = strings.TrimSpace(e.IPv6)
		if e.Domain == "" {
			return errors.New("domain is required")
		}
		if e.IP == "" && e.IPv6 == "" {
			return errors.New("IPv4 or IPv6 address is required")
		}
		if e.IP != "" {
			ip := net.ParseIP(e.IP)
			if ip == nil || ip.To4() == nil {
				return errors.New("invalid IPv4 address")
			}
			e.IP = ip.To4().String()
		}
		if e.IPv6 != "" {
			ip := net.ParseIP(e.IPv6)
			if ip == nil || ip.To4() != nil {
				return errors.New("invalid IPv6 address")
			}
			e.IPv6 = ip.String()
		}
		if seen[e.Domain] {
			return errors.New("Two entries can't have the same domain")
		}
		seen[e.Domain] = true
	}
	return nil
}

func Error(s string) {
	panic("unimplemented")
}
