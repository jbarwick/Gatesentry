package gatesentryDnsServer

import (
	"net"
	"strings"
	"sync/atomic"

	"github.com/miekg/dns"
)

// customHost is a pre-parsed operator-configured name (Settings → Custom A/AAAA).
type customHost struct {
	A    net.IP // 4-byte IPv4, or nil
	AAAA net.IP // 16-byte IPv6, or nil
}

// customTable is an immutable snapshot swapped atomically on reload.
type customTable struct {
	byName map[string]customHost // name without trailing dot
	byPTR  map[string]string     // in-addr.arpa / ip6.arpa → name
}

var customRecords atomic.Value // *customTable

func emptyCustomTable() *customTable {
	return &customTable{
		byName: make(map[string]customHost),
		byPTR:  make(map[string]string),
	}
}

func init() {
	customRecords.Store(emptyCustomTable())
}

func loadCustomTable() *customTable {
	t, _ := customRecords.Load().(*customTable)
	if t == nil {
		return emptyCustomTable()
	}
	return t
}

// ReplaceCustomRecords publishes a new custom-record snapshot for the query
// path. Called after settings load. Safe for concurrent DNS readers.
func ReplaceCustomRecords(ipv4, ipv6 map[string]string) {
	t := emptyCustomTable()
	if ipv4 != nil {
		t.byName = make(map[string]customHost, len(ipv4)+len(ipv6))
	}
	add := func(name, ip string, v6 bool) {
		name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
		if name == "" || ip == "" {
			return
		}
		parsed := net.ParseIP(ip)
		if parsed == nil {
			return
		}
		h := t.byName[name]
		if v6 {
			if parsed.To4() != nil {
				return
			}
			h.AAAA = parsed.To16()
		} else {
			v4 := parsed.To4()
			if v4 == nil {
				return
			}
			h.A = v4
		}
		t.byName[name] = h
		if arpa, err := dns.ReverseAddr(ip); err == nil {
			t.byPTR[strings.ToLower(strings.TrimSuffix(arpa, "."))] = name
		}
	}
	for n, ip := range ipv4 {
		add(n, ip, false)
	}
	for n, ip := range ipv6 {
		add(n, ip, true)
	}
	customRecords.Store(t)
}

func lookupCustom(name string) (customHost, bool) {
	h, ok := loadCustomTable().byName[name]
	return h, ok
}

func lookupCustomPTR(reverseName string) string {
	return loadCustomTable().byPTR[reverseName]
}

func customAnswers(qname string, qtype uint16, h customHost) []dns.RR {
	var out []dns.RR
	wantA := qtype == dns.TypeA || qtype == dns.TypeANY
	wantAAAA := qtype == dns.TypeAAAA || qtype == dns.TypeANY
	if wantA && h.A != nil {
		out = append(out, &dns.A{
			Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   h.A,
		})
	}
	if wantAAAA && h.AAAA != nil {
		out = append(out, &dns.AAAA{
			Hdr:  dns.RR_Header{Name: qname, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
			AAAA: h.AAAA,
		})
	}
	return out
}
