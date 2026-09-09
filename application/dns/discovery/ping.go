package discovery

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	defaultPingTimeout   = time.Second
	maxConcurrentPings   = 16
	pingPayload          = "gatesentry-ping"
	execPingLookPathName = "ping"
)

var (
	errInvalidIP       = errors.New("invalid IP address")
	errPingUnsupported = errors.New("ping is not available")
	errUnexpectedICMP  = errors.New("unexpected ICMP reply")

	pingSupportOnce sync.Once
	icmp4Network    string
	icmp4Listen     string
	icmp6Network    string
	icmp6Listen     string
	execPingExists  bool
)

// pingFunc probes a host and returns RTT on success.
type pingFunc func(ctx context.Context, ip string, timeout time.Duration) (time.Duration, error)

// PingSupported reports whether ICMP or a system ping binary can be used.
func PingSupported() bool {
	return pingSupported()
}

func pingSupported() bool {
	discoverPingSupport()
	return icmp4Network != "" || icmp6Network != "" || execPingExists
}

func discoverPingSupport() {
	pingSupportOnce.Do(func() {
		if tryICMPListen("udp4", "0.0.0.0") {
			icmp4Network, icmp4Listen = "udp4", "0.0.0.0"
		} else if tryICMPListen("ip4:icmp", "0.0.0.0") {
			icmp4Network, icmp4Listen = "ip4:icmp", "0.0.0.0"
		}
		if tryICMPListen("udp6", "::") {
			icmp6Network, icmp6Listen = "udp6", "::"
		} else if tryICMPListen("ip6:ipv6-icmp", "::") {
			icmp6Network, icmp6Listen = "ip6:ipv6-icmp", "::"
		}
		_, err := exec.LookPath(execPingLookPathName)
		execPingExists = err == nil
	})
}

func tryICMPListen(network, address string) bool {
	c, err := icmp.ListenPacket(network, address)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func pingHost(ctx context.Context, ip string, timeout time.Duration) (time.Duration, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return 0, errInvalidIP
	}

	discoverPingSupport()

	v4 := parsed.To4() != nil
	if v4 && icmp4Network != "" {
		return pingICMP(ctx, parsed, timeout)
	}
	if !v4 && icmp6Network != "" {
		return pingICMP(ctx, parsed, timeout)
	}
	if execPingExists {
		return pingExec(ctx, ip, timeout)
	}
	return 0, errPingUnsupported
}

func pingICMP(ctx context.Context, ip net.IP, timeout time.Duration) (time.Duration, error) {
	network, listen, dst, icmpType, proto := icmpParams(ip)
	if network == "" {
		return 0, errPingUnsupported
	}

	c, err := icmp.ListenPacket(network, listen)
	if err != nil {
		return 0, err
	}
	defer func() { _ = c.Close() }()

	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	if err := c.SetDeadline(deadline); err != nil {
		return 0, err
	}

	msg := icmp.Message{
		Type: icmpType,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Seq:  int(time.Now().UnixNano() & 0xffff),
			Data: []byte(pingPayload),
		},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	if _, err := c.WriteTo(wb, dst); err != nil {
		return 0, err
	}

	rb := make([]byte, 1500)
	n, _, err := c.ReadFrom(rb)
	if err != nil {
		return 0, err
	}

	rm, err := icmp.ParseMessage(proto, rb[:n])
	if err != nil {
		return 0, err
	}
	switch rm.Type {
	case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
		return time.Since(start), nil
	default:
		return 0, errUnexpectedICMP
	}
}

func icmpParams(ip net.IP) (network, listen string, dst net.Addr, icmpType icmp.Type, proto int) {
	if ip.To4() != nil {
		ip4 := ip.To4()
		if icmp4Network == "" {
			return "", "", nil, nil, 0
		}
		if strings.HasPrefix(icmp4Network, "udp") {
			return icmp4Network, icmp4Listen, &net.UDPAddr{IP: ip4}, ipv4.ICMPTypeEcho, 1
		}
		return icmp4Network, icmp4Listen, &net.IPAddr{IP: ip4}, ipv4.ICMPTypeEcho, 1
	}
	if icmp6Network == "" {
		return "", "", nil, nil, 0
	}
	if strings.HasPrefix(icmp6Network, "udp") {
		return icmp6Network, icmp6Listen, &net.UDPAddr{IP: ip}, ipv6.ICMPTypeEchoRequest, 58
	}
	return icmp6Network, icmp6Listen, &net.IPAddr{IP: ip}, ipv6.ICMPTypeEchoRequest, 58
}

func pingExec(ctx context.Context, ip string, timeout time.Duration) (time.Duration, error) {
	if !execPingExists {
		return 0, errPingUnsupported
	}

	secs := int(timeout.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout+500*time.Millisecond)
	defer cancel()

	// iputils and BusyBox both accept -c 1 -W <seconds>.
	args := []string{"-c", "1", "-W", strconv.Itoa(secs)}
	if strings.Contains(ip, ":") {
		args = append(args, "-6")
	}
	args = append(args, ip)

	//nolint:gosec // G204: ip is net.ParseIP-validated; argv is not passed to a shell
	cmd := exec.CommandContext(cmdCtx, execPingLookPathName, args...)
	start := time.Now()
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}
