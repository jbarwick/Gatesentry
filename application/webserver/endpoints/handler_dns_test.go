package gatesentryWebserverEndpoints

import (
	"testing"

	gatesentryTypes "bitbucket.org/abdullah_irfan/gatesentryf/types"
)

func TestNormalizeCustomDNSEntries(t *testing.T) {
	entries := []gatesentryTypes.DNSCustomEntry{
		{Domain: "Host.Example.COM.", IP: "192.0.2.10"},
		{Domain: "v6only", IPv6: "2001:db8::1"},
		{Domain: "both", IP: "192.0.2.20", IPv6: "2001:db8::2"},
	}
	if err := normalizeCustomDNSEntries(entries); err != nil {
		t.Fatal(err)
	}
	if entries[0].Domain != "host.example.com" || entries[0].IP != "192.0.2.10" {
		t.Errorf("v4 entry = %+v", entries[0])
	}
	if entries[1].IPv6 != "2001:db8::1" {
		t.Errorf("v6 entry = %+v", entries[1])
	}
}

func TestNormalizeCustomDNSEntriesRequiresAddress(t *testing.T) {
	err := normalizeCustomDNSEntries([]gatesentryTypes.DNSCustomEntry{
		{Domain: "empty"},
	})
	if err == nil {
		t.Fatal("expected error when neither address is set")
	}
}

func TestNormalizeCustomDNSEntriesRejectsBadIPv6(t *testing.T) {
	err := normalizeCustomDNSEntries([]gatesentryTypes.DNSCustomEntry{
		{Domain: "bad", IPv6: "192.0.2.1"},
	})
	if err == nil {
		t.Fatal("expected error for IPv4 in ipv6 field")
	}
}

func TestNormalizeCustomDNSEntriesDuplicateDomain(t *testing.T) {
	err := normalizeCustomDNSEntries([]gatesentryTypes.DNSCustomEntry{
		{Domain: "dup", IP: "192.0.2.1"},
		{Domain: "DUP.", IPv6: "2001:db8::1"},
	})
	if err == nil {
		t.Fatal("expected duplicate domain error")
	}
}
