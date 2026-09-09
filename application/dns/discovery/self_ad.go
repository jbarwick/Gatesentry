package discovery

import (
	"net"
	"os"
	"strings"
)

// Bonjour instance names this process registers (application/bonjour.go).
func isOwnMDNSInstance(instance string) bool {
	s := strings.TrimSpace(instance)
	if s == "" {
		return false
	}
	if strings.EqualFold(s, "GateSentry") {
		return true
	}
	return strings.HasPrefix(strings.ToLower(s), "gatesentry ")
}

func selfHostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return ""
	}
	return hostnameKey(h)
}

func isLocalIP(ip string) bool {
	if ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP == nil {
			continue
		}
		if ipnet.IP.Equal(parsed) {
			return true
		}
	}
	return false
}

// isOwnAdvertisement reports an mDNS entry that is this process advertising
// itself. Those must not be applied to some other host's address.
func isOwnAdvertisement(instance, hostname, ipv4, ipv6 string) bool {
	if !isOwnMDNSInstance(instance) && hostnameKey(hostname) != selfHostname() {
		return false
	}
	if ipv4 != "" && isLocalIP(ipv4) {
		return true
	}
	if ipv6 != "" && isLocalIP(ipv6) {
		return true
	}
	// Our instance name (or our hostname) at an address that is not ours.
	return isOwnMDNSInstance(instance) || hostnameKey(hostname) == selfHostname()
}

// stripForeignSelfAdvertisements removes this process's Bonjour identity
// from devices that are not on a local interface address.
func (ds *DeviceStore) stripForeignSelfAdvertisements() {
	self := selfHostname()
	for _, d := range ds.devices {
		if (d.IPv4 != "" && isLocalIP(d.IPv4)) || (d.IPv6 != "" && isLocalIP(d.IPv6)) {
			continue
		}
		hadSelf := false
		keptMDNS := d.MDNSNames[:0:0]
		for _, m := range d.MDNSNames {
			if isOwnMDNSInstance(m) {
				hadSelf = true
				continue
			}
			keptMDNS = append(keptMDNS, m)
		}
		d.MDNSNames = keptMDNS
		if !hadSelf {
			continue
		}
		keptHosts := d.Hostnames[:0:0]
		for _, h := range d.Hostnames {
			if self != "" && hostnameKey(h) == self {
				continue
			}
			keptHosts = append(keptHosts, h)
		}
		d.Hostnames = keptHosts
		ds.refreshDerivedNames(d)
	}
}
