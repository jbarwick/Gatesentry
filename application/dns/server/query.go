package gatesentryDnsServer

import (
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"bitbucket.org/abdullah_irfan/gatesentryf/dns/discovery"
	"github.com/miekg/dns"
)

var dnsDebug atomic.Bool

func init() {
	dnsDebug.Store(os.Getenv("GS_DEBUG_LOGGING") == "true")
}

// Query order (local answers before any filter or recursive lookup):
//
//  1. Custom A/AAAA (operator-configured) — lock-free snapshot
//  2. This appliance's own names
//  3. Device inventory
//  4. WPAD
//  5. Allow-list exception, then block lists
//  6. Response cache, then upstream
func handleDNSRequest(w dns.ResponseWriter, r *dns.Msg) {
	queryStart := time.Now()
	dnsMetrics.QueriesTotal.Add(1)
	defer func() { dnsMetrics.QueryDuration.Observe(time.Since(queryStart)) }()

	if !serverRunning.Load() {
		w.Close()
		return
	}

	if r.Opcode == dns.OpcodeUpdate {
		dnsMetrics.QueriesDDNS.Add(1)
		handleDDNSUpdate(w, r)
		return
	}

	if deviceStore != nil {
		if clientIP := discovery.ExtractClientIP(w.RemoteAddr()); clientIP != "" {
			go deviceStore.ObservePassiveQuery(clientIP)
		}
	}

	for _, q := range r.Question {
		domain := strings.ToLower(strings.TrimSuffix(q.Name, "."))
		if domain == "" {
			continue
		}
		qtype := q.Qtype

		// 1. Custom records — before filters and before recursive search.
		if qtype == dns.TypePTR {
			if name := lookupCustomPTR(domain); name != "" {
				dnsMetrics.QueriesInternal.Add(1)
				replyAuth(w, r, []dns.RR{&dns.PTR{
					Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 60},
					Ptr: dns.Fqdn(name),
				}})
				noteQuery(domain, qtype, "internal", false)
				return
			}
		} else if h, ok := lookupCustom(domain); ok {
			dnsMetrics.QueriesInternal.Add(1)
			replyAuth(w, r, customAnswers(q.Name, qtype, h))
			noteQuery(domain, qtype, "internal", false)
			return
		}

		// 2. This host
		if isApplianceName(domain) {
			if answers := selfAnswers(q.Name, qtype); len(answers) > 0 {
				replyAuth(w, r, answers)
				noteQuery(domain, qtype, "self", false)
				return
			}
		}

		// 3. Device inventory
		if deviceStore != nil {
			var records []discovery.DnsRecord
			if qtype == dns.TypePTR && isReverseDomain(domain) {
				records = deviceStore.LookupReverse(domain)
			} else {
				records = deviceStore.LookupName(domain, qtype)
			}
			if len(records) > 0 {
				dnsMetrics.QueriesDevice.Add(1)
				answers := make([]dns.RR, 0, len(records))
				for i := range records {
					if rr := records[i].ToRR(); rr != nil {
						answers = append(answers, rr)
					}
				}
				replyAuth(w, r, answers)
				noteQuery(domain, qtype, "device", false)
				return
			}
		}

		// 4. WPAD
		if wpadEnabled.Load() && (domain == "wpad" || strings.HasPrefix(domain, "wpad.")) {
			if answers := selfAnswers(q.Name, qtype); len(answers) > 0 {
				dnsMetrics.QueriesWPAD.Add(1)
				replyAuth(w, r, answers)
				noteQuery(domain, qtype, "wpad", false)
				return
			}
		}

		// 5. Filters (allow exception, then block lists)
		mutex.RLock()
		isException := exceptionDomains[domain]
		mutex.RUnlock()
		if isException {
			dnsMetrics.QueriesException.Add(1)
			noteQuery(domain, qtype, "exception", false)
		} else if dnsFilteringEnabled.Load() && isDomainBlocked(domain) {
			dnsMetrics.QueriesBlocked.Add(1)
			answers := selfAnswers(q.Name, qtype)
			if len(answers) == 0 && localIp != "" {
				answers = append(answers, &dns.A{
					Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
					A:   net.ParseIP(localIp),
				})
			}
			replyAuth(w, r, answers)
			noteQuery(domain, qtype, "blocked", true)
			return
		}

		// 6. Cache then upstream
		if dnsResponseCache != nil {
			if cached := dnsResponseCache.Get(q.Name, qtype); cached != nil {
				dnsMetrics.QueriesCached.Add(1)
				cached.SetReply(r)
				cached.Authoritative = false
				noteQuery(domain, qtype, "cached", false)
				w.WriteMsg(cached)
				return
			}
		}

		useTCP := w.LocalAddr().Network() == "tcp"
		resp, err := forwardDNSRequest(r, useTCP)
		if err != nil {
			dnsMetrics.QueriesError.Add(1)
			if dnsResponseCache != nil {
				dnsResponseCache.PutFailure(q.Name, qtype, dns.RcodeServerFailure)
			}
			errMsg := new(dns.Msg)
			errMsg.SetRcode(r, dns.RcodeServerFailure)
			noteQuery(domain, qtype, "error", false)
			w.WriteMsg(errMsg)
			return
		}

		if dnsResponseCache != nil {
			dnsResponseCache.Put(q.Name, qtype, resp)
		}
		dnsMetrics.QueriesForwarded.Add(1)
		noteQuery(domain, qtype, "forwarded", false)

		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = false
		m.Rcode = resp.Rcode
		m.Answer = append(m.Answer, resp.Answer...)
		m.Ns = append(m.Ns, resp.Ns...)
		w.WriteMsg(m)
		return
	}
}

func replyAuth(w dns.ResponseWriter, req *dns.Msg, answers []dns.RR) {
	m := new(dns.Msg)
	m.SetRcode(req, dns.RcodeSuccess)
	m.Authoritative = true
	m.Answer = answers
	w.WriteMsg(m)
}

func noteQuery(domain string, qtype uint16, kind string, blocked bool) {
	emitRequestEvent(domain, qtypeName(qtype), kind, blocked)
	if dnsDebug.Load() && logger != nil {
		logger.LogDNS(domain, "dns", kind)
	}
}

func qtypeName(t uint16) string {
	switch t {
	case dns.TypeA:
		return "A"
	case dns.TypeAAAA:
		return "AAAA"
	case dns.TypePTR:
		return "PTR"
	case dns.TypeHTTPS:
		return "HTTPS"
	default:
		if s := dns.TypeToString[t]; s != "" {
			return s
		}
		return "TYPE"
	}
}
