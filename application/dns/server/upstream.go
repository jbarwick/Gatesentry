package gatesentryDnsServer

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

const (
	maxInFlightForwards  = 32
	circuitFailThreshold = 8
	circuitOpenDuration  = 10 * time.Second
)

var (
	forwardSem        = make(chan struct{}, maxInFlightForwards)
	consecutiveFails  atomic.Int32
	circuitUntilNano  atomic.Int64
	resolverValue     atomic.Value // IPv4 upstream host:port
	resolverIPv6Value atomic.Value // IPv6 upstream [host]:port
)

func init() {
	resolverValue.Store("8.8.8.8:53")
	resolverIPv6Value.Store("[fd00:1234:5678::1]:53")
}

func currentResolver() string {
	if v, ok := resolverValue.Load().(string); ok && v != "" {
		return v
	}
	return "8.8.8.8:53"
}

func currentResolverIPv6() string {
	if v, ok := resolverIPv6Value.Load().(string); ok && v != "" {
		return v
	}
	return currentResolver()
}

func circuitIsOpen() bool {
	until := circuitUntilNano.Load()
	return until > 0 && time.Now().UnixNano() < until
}

func noteUpstreamFailure() {
	n := consecutiveFails.Add(1)
	if n >= circuitFailThreshold {
		circuitUntilNano.Store(time.Now().Add(circuitOpenDuration).UnixNano())
	}
}

func noteUpstreamSuccess() {
	consecutiveFails.Store(0)
	circuitUntilNano.Store(0)
}

// ResetUpstreamCircuit is for tests.
func ResetUpstreamCircuit() {
	consecutiveFails.Store(0)
	circuitUntilNano.Store(0)
}

func tryAcquireForward() bool {
	select {
	case forwardSem <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseForward() {
	select {
	case <-forwardSem:
	default:
	}
}

func resolverForMsg(r *dns.Msg) string {
	if r == nil || len(r.Question) == 0 {
		return currentResolver()
	}
	q := r.Question[0]
	switch q.Qtype {
	case dns.TypeAAAA, dns.TypeSVCB, dns.TypeHTTPS:
		return currentResolverIPv6()
	case dns.TypePTR:
		name := strings.ToLower(q.Name)
		if strings.HasSuffix(name, ".ip6.arpa.") || strings.HasSuffix(name, ".ip6.arpa") {
			return currentResolverIPv6()
		}
	}
	return currentResolver()
}
