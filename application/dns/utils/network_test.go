package gatesentryDnsUtils

import "testing"

func TestGetLocalIPDoesNotPanic(t *testing.T) {
	ip, err := GetLocalIP()
	if err != nil {
		t.Logf("no IPv4 on this host: %v", err)
		return
	}
	if ip == "" {
		t.Fatal("empty IPv4")
	}
}

func TestGetLocalIPv6DoesNotPanic(t *testing.T) {
	ip, err := GetLocalIPv6()
	if err != nil {
		t.Logf("no IPv6 on this host: %v", err)
		return
	}
	if ip == "" {
		t.Fatal("empty IPv6")
	}
}
