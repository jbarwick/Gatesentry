package gatesentryDnsServer

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dnscache "bitbucket.org/abdullah_irfan/gatesentryf/dns/cache"
	"bitbucket.org/abdullah_irfan/gatesentryf/dns/discovery"
	gatesentryDnsScheduler "bitbucket.org/abdullah_irfan/gatesentryf/dns/scheduler"
	gatesentryDnsUtils "bitbucket.org/abdullah_irfan/gatesentryf/dns/utils"
	gatesentryDomainList "bitbucket.org/abdullah_irfan/gatesentryf/domainlist"
	gatesentryLogger "bitbucket.org/abdullah_irfan/gatesentryf/logger"
	gatesentry2storage "bitbucket.org/abdullah_irfan/gatesentryf/storage"
	gatesentryTypes "bitbucket.org/abdullah_irfan/gatesentryf/types"
	"github.com/miekg/dns"
)

// normalizeResolver ensures the resolver address has a port suffix
// If no port is specified, :53 is appended
// Properly handles IPv6 addresses (e.g., [2001:4860:4860::8888]:53)
func normalizeResolver(resolver string) string {
	if resolver == "" {
		return "8.8.8.8:53"
	}
	// Try to split host and port - if it fails, no port is specified
	host, port, err := net.SplitHostPort(resolver)
	if err != nil {
		// No port specified (or invalid format), add default port
		// net.JoinHostPort handles IPv6 bracketing automatically
		return net.JoinHostPort(resolver, "53")
	}
	// Port was specified, return as-is (already valid format)
	if port == "" {
		return net.JoinHostPort(host, "53")
	}
	return resolver
}

type QueryLog struct {
	Domain string
	Time   time.Time
}

// Environment variable names for DNS server configuration
const (
	// ENV_DNS_LISTEN_ADDR sets the IP address to bind the DNS server (default: 0.0.0.0)
	ENV_DNS_LISTEN_ADDR = "GATESENTRY_DNS_ADDR"
	// ENV_DNS_LISTEN_PORT sets the port for UDP/TCP DNS server (default: 53)
	ENV_DNS_LISTEN_PORT = "GATESENTRY_DNS_PORT"
	// ENV_DNS_EXTERNAL_RESOLVER sets the external DNS resolver (default: 8.8.8.8:53)
	ENV_DNS_EXTERNAL_RESOLVER = "GATESENTRY_DNS_RESOLVER"
)

var (
	externalResolver = "8.8.8.8:53"
	listenAddr       = "0.0.0.0"
	listenPort       = "53"
	// RWMutex allows concurrent reads while blocking writes.
	// Use RLock() for reading blockedDomains/exceptionDomains/internalRecords
	// Use Lock() when updating these maps (in scheduler/filter initialization)
	mutex            sync.RWMutex
	blockedDomains   = make(map[string]bool)
	exceptionDomains = make(map[string]bool)
	internalRecords  = make(map[string]string)
	localIp, _       = gatesentryDnsUtils.GetLocalIP()
	localIPv6, _     = gatesentryDnsUtils.GetLocalIPv6()
	localIps         = gatesentryDnsUtils.GetLocalIPs()
	localIPv6s       = gatesentryDnsUtils.GetLocalIPv6s()
	queryLogs        = make(map[string][]QueryLog)
	logMutex         sync.Mutex
	logsFile         *os.File
	fileMutex        sync.Mutex
	logsPath         = "dns_logs.txt"
	logger           *gatesentryLogger.Log
)

// wpadEnabled controls whether the DNS server intercepts wpad.* queries
// and returns GateSentry's own IP. Enabled by default.
// Toggled via SetWPADEnabled() from the settings API.
var wpadEnabled atomic.Bool

// dnsFilteringEnabled controls whether the DNS server blocks domains on
// the blocklist. When disabled, the DNS server stays running and continues
// to resolve all queries (logging stats, populating charts, caching, etc.)
// but blocked domains are forwarded to the upstream resolver instead of
// returning GateSentry's IP.  Enabled by default.
var dnsFilteringEnabled atomic.Bool

// domainListMgr is the shared DomainListManager, set during StartDNSServer.
// Used for O(1) domain blocklist/whitelist lookups via the shared index.
var domainListMgr *gatesentryDomainList.DomainListManager

// settings is the shared settings store, set during StartDNSServer.
// Used to read dns_domain_lists and dns_whitelist_domain_lists.
var dnsSettings *gatesentry2storage.MapStore

// Cached domain-list IDs so isDomainBlocked does not unmarshal GSSettings
// on every query.
var (
	cachedBlockListIDs atomic.Value // []string
	cachedAllowListIDs atomic.Value // []string
)

// Appliance hostnames answered locally so the admin UI does not depend on
// upstream DNS.
var (
	selfNamesMu sync.RWMutex
	selfNames   = map[string]bool{}
)

func init() {
	// WPAD DNS interception is enabled by default
	wpadEnabled.Store(true)
	// DNS domain filtering is enabled by default
	dnsFilteringEnabled.Store(true)
}

// SetWPADEnabled enables or disables WPAD DNS interception.
func SetWPADEnabled(enabled bool) {
	wpadEnabled.Store(enabled)
	if enabled {
		log.Printf("[DNS] WPAD interception enabled (local IP: %s)", localIp)
	} else {
		log.Println("[DNS] WPAD interception disabled")
	}
}

// IsWPADEnabled returns true if WPAD DNS interception is active.
func IsWPADEnabled() bool {
	return wpadEnabled.Load()
}

// SetDNSFilteringEnabled enables or disables DNS domain blocking.
// When disabled the DNS server still runs, resolves all queries, and
// records stats — but blocked domains are forwarded to the upstream
// resolver instead of being intercepted.
func SetDNSFilteringEnabled(enabled bool) {
	dnsFilteringEnabled.Store(enabled)
	if enabled {
		log.Println("[DNS] Domain filtering enabled")
	} else {
		log.Println("[DNS] Domain filtering disabled — blocked domains will resolve normally")
	}
}

// IsDNSFilteringEnabled returns true if DNS domain blocking is active.
func IsDNSFilteringEnabled() bool {
	return dnsFilteringEnabled.Load()
}

// dnsResponseCache is the sharded, TTL-aware DNS response cache.
// Initialised in StartDNSServer() with environment-driven configuration.
// Accessible via GetDNSCache() for the SSE endpoint and admin API.
var dnsResponseCache *dnscache.DNSCache

// cacheRecorder periodically snapshots cache counters into BuntDB.
// Initialised in StartDNSServer() alongside the cache itself.
var cacheRecorder *dnscache.Recorder

// GetDNSCache returns the DNS response cache instance.
// Returns nil before StartDNSServer() is called.
func GetDNSCache() *dnscache.DNSCache {
	return dnsResponseCache
}

// GetCacheRecorder returns the cache stats recorder, or nil if not started.
func GetCacheRecorder() *dnscache.Recorder {
	return cacheRecorder
}

// emitRequestEvent sends a high-level DNS request event to any SSE subscribers.
// Safe to call when the cache or event bus is nil (no-op).
func emitRequestEvent(domain, qtype, responseType string, blocked bool) {
	if dnsResponseCache != nil && dnsResponseCache.Events != nil {
		dnsResponseCache.Events.Emit(dnscache.RequestEvent(domain, qtype, responseType, blocked))
	}
}

func init() {
	// Load configuration from environment variables
	if envAddr := os.Getenv(ENV_DNS_LISTEN_ADDR); envAddr != "" {
		listenAddr = envAddr
		log.Printf("[DNS] Using listen address from environment: %s", listenAddr)
	}
	if envPort := os.Getenv(ENV_DNS_LISTEN_PORT); envPort != "" {
		listenPort = envPort
		log.Printf("[DNS] Using listen port from environment: %s", listenPort)
	}
	if envResolver := os.Getenv(ENV_DNS_EXTERNAL_RESOLVER); envResolver != "" {
		externalResolver = normalizeResolver(envResolver)
		log.Printf("[DNS] Using external resolver from environment: %s", externalResolver)
	}
}

// GetListenAddr returns the current DNS listen address
func GetListenAddr() string {
	return listenAddr
}

// SetListenAddr sets the DNS listen address
func SetListenAddr(addr string) {
	if addr != "" {
		listenAddr = addr
	}
}

// GetListenPort returns the current DNS listen port
func GetListenPort() string {
	return listenPort
}

// SetListenPort sets the DNS listen port
func SetListenPort(port string) {
	if port != "" {
		listenPort = port
	}
}

func SetExternalResolver(resolver string) {
	if resolver != "" {
		r := normalizeResolver(resolver)
		externalResolver = r
		resolverValue.Store(r)
	}
}

func SetExternalResolverIPv6(resolver string) {
	if resolver != "" {
		resolverIPv6Value.Store(normalizeResolver(resolver))
	}
}

var server *dns.Server    // UDP server (primary listen address)
var tcpServer *dns.Server // TCP server for large queries (>512 bytes)
var extraDNSServers []*dns.Server
var serverRunning atomic.Bool // Thread-safe flag for server state
var restartDnsSchedulerChan chan bool

// deviceStore is the central device inventory and DNS record store.
// Discovery sources populate it; handleDNSRequest reads from it.
// Initialized in StartDNSServer().
var deviceStore *discovery.DeviceStore

// mdnsBrowser performs periodic mDNS/Bonjour scanning to discover devices.
// Initialized in StartDNSServer() when mDNS browsing is enabled.
var mdnsBrowser *discovery.MDNSBrowser

// GetDeviceStore returns the global device store for use by discovery sources,
// the API layer, and other packages. Returns nil before StartDNSServer is called.
func GetDeviceStore() *discovery.DeviceStore {
	return deviceStore
}

// GetMDNSBrowser returns the global mDNS browser instance, or nil if not started.
func GetMDNSBrowser() *discovery.MDNSBrowser {
	return mdnsBrowser
}

const BLOCKLIST_HOURLY_UPDATE_INTERVAL = 10

// SetDNSZones updates the DNS zones at runtime. Parses a comma-separated
// zone string, ensures "local" is always included (required for mDNS/Bonjour),
// and rebuilds all device DNS records for the new zone set.
func SetDNSZones(zoneSetting string) {
	if deviceStore == nil {
		return
	}
	zones := parseDNSZones(zoneSetting)
	deviceStore.SetZones(zones)
	log.Printf("[DNS] Zones updated: %v (primary: %s)", zones, zones[0])
}

// parseDNSZones splits a comma-separated zone string into a slice,
// ensuring "local" is always present. The first user-specified zone
// becomes the primary; "local" is appended if not already listed.
func parseDNSZones(zoneSetting string) []string {
	var zones []string
	hasLocal := false
	for _, z := range strings.Split(zoneSetting, ",") {
		z = strings.TrimSpace(z)
		if z == "" {
			continue
		}
		if strings.EqualFold(z, "local") {
			hasLocal = true
		}
		zones = append(zones, z)
	}
	if !hasLocal {
		zones = append(zones, "local")
	}
	if len(zones) == 0 {
		zones = []string{"local"}
	}
	return zones
}

func StartDNSServer(basePath string, ilogger *gatesentryLogger.Log, blockedLists []string, settings *gatesentry2storage.MapStore, dnsinfo *gatesentryTypes.DnsServerInfo, dlManager *gatesentryDomainList.DomainListManager) {

	if server != nil || serverRunning.Load() {
		fmt.Println("DNS server is already running")
		restartDnsSchedulerChan <- true
		return
	}

	logger = ilogger
	logsPath = basePath + logsPath
	SetExternalResolver(settings.Get("dns_resolver"))
	SetExternalResolverIPv6(settings.Get("dns_resolver_ipv6"))

	// Store shared references for use in handleDNSRequest
	domainListMgr = dlManager
	dnsSettings = settings
	RefreshDNSListIDs()
	registerApplianceNames(settings)
	// InitializeLogs()
	// go gatesentryDnsFilter.InitializeBlockedDomains(&blockedDomains, &blockedLists)

	// Initialize the device store with configured zones (default: "local").
	// Supports multiple comma-separated zones for split-horizon DNS.
	// Example: "jvj28.com,local" → devices resolve as both
	//   macmini.jvj28.com AND macmini.local
	// The first zone is the primary (used for PTR targets).
	// "local" is always included for mDNS/Bonjour compatibility.
	zoneSetting := settings.Get("dns_local_zone")
	if zoneSetting == "" {
		zoneSetting = "local"
		settings.Update("dns_local_zone", zoneSetting)
	}
	zones := parseDNSZones(zoneSetting)
	deviceStore = discovery.NewDeviceStoreMultiZone(zones...)
	deviceStore.SetPersistPath(filepath.Join(basePath, "devices.json"))
	log.Printf("[DNS] Device store initialized with zones: %v (primary: %s)", zones, zones[0])

	// Initialise the DNS response cache with environment-tuneable limits.
	// GS_DNS_CACHE_MAX overrides the default 10,000 entry limit.
	cacheCfg := dnscache.DefaultConfig()
	if maxStr := os.Getenv("GS_DNS_CACHE_MAX"); maxStr != "" {
		var maxVal int
		if _, err := fmt.Sscan(maxStr, &maxVal); err == nil && maxVal > 0 {
			cacheCfg.MaxEntries = maxVal
			log.Printf("[DNS Cache] Max entries set to %d via GS_DNS_CACHE_MAX", maxVal)
		}
	}
	dnsResponseCache = dnscache.New(cacheCfg)
	log.Printf("[DNS Cache] Initialised: max=%d, minTTL=%s, maxTTL=%s, negativeTTL=%s, reap=%s",
		cacheCfg.MaxEntries, cacheCfg.MinTTL, cacheCfg.MaxTTL, cacheCfg.NegativeTTL, cacheCfg.ReapInterval)

	// Start the periodic cache stats recorder (1 write/min → BuntDB, 24h TTL).
	// Uses the existing logger's DB so snapshots survive binary restarts.
	if logger != nil && logger.Database != nil {
		cacheRecorder = dnscache.NewRecorder(dnsResponseCache, logger.Database, time.Minute)
		cacheRecorder.Start()
	}

	// Start mDNS/Bonjour browser for automatic device discovery (Phase 3).
	// Browses common service types (_airplay._tcp, _googlecast._tcp, _printer._tcp, etc.)
	// and feeds discovered devices into the device store.
	// Enabled by default. Set setting "mdns_browser_enabled" to "false" to disable.
	mdnsEnabled := settings.Get("mdns_browser_enabled")
	if mdnsEnabled != "false" {
		mdnsBrowser = discovery.NewMDNSBrowser(deviceStore, discovery.DefaultScanInterval)
		mdnsBrowser.Start()
	}

	// Configure DDNS (Phase 4: RFC 2136 Dynamic DNS UPDATE handler).
	// Settings: ddns_enabled, ddns_tsig_required, ddns_tsig_key_name,
	//           ddns_tsig_key_secret, ddns_tsig_algorithm
	ddnsEnabledStr := settings.Get("ddns_enabled")
	if ddnsEnabledStr == "" {
		// New installs: DDNS off until the admin enables it (TSIG recommended).
		ddnsEnabledStr = "false"
		settings.Update("ddns_enabled", ddnsEnabledStr)
	}
	ddnsEnabled = ddnsEnabledStr == "true"

	ddnsTSIGRequiredStr := settings.Get("ddns_tsig_required")
	if ddnsTSIGRequiredStr == "" {
		// Seed default: TSIG not required
		ddnsTSIGRequiredStr = "false"
		settings.Update("ddns_tsig_required", ddnsTSIGRequiredStr)
	}
	ddnsTSIGRequired = ddnsTSIGRequiredStr == "true"

	// Build TSIG secret map for server-level TSIG verification.
	// The miekg/dns server automatically verifies TSIG on incoming messages
	// when TsigSecret is set, and exposes the result via w.TsigStatus().
	var tsigSecrets map[string]string
	tsigKeyName := settings.Get("ddns_tsig_key_name")
	tsigKeySecret := settings.Get("ddns_tsig_key_secret")
	if tsigKeyName != "" && tsigKeySecret != "" {
		if !strings.HasSuffix(tsigKeyName, ".") {
			tsigKeyName += "."
		}
		tsigSecrets = map[string]string{tsigKeyName: tsigKeySecret}
		log.Printf("[DDNS] TSIG configured: key=%s", strings.TrimSuffix(tsigKeyName, "."))
	}

	if ddnsEnabled {
		log.Printf("[DDNS] Dynamic DNS updates enabled (TSIG required: %v)", ddnsTSIGRequired)
	} else {
		log.Println("[DDNS] Dynamic DNS updates disabled")
	}

	restartDnsSchedulerChan = make(chan bool)

	go gatesentryDnsScheduler.RunScheduler(
		&blockedDomains,
		&blockedLists,
		&internalRecords,
		&exceptionDomains,
		&mutex,
		settings,
		dnsinfo,
		BLOCKLIST_HOURLY_UPDATE_INTERVAL,
		restartDnsSchedulerChan,
		dlManager,
	)
	restartDnsSchedulerChan <- true

	serverRunning.Store(true)
	addrs := dnsListenAddrs()
	primary := net.JoinHostPort(addrs[0], listenPort)

	tcpServer = &dns.Server{
		Addr:          primary,
		Net:           "tcp",
		MsgAcceptFunc: ddnsMsgAcceptFunc,
		TsigSecret:    tsigSecrets,
		Handler:       dns.HandlerFunc(handleDNSRequest),
	}
	go func() {
		fmt.Printf("DNS forwarder listening on %s (TCP). Handles large queries >512 bytes.\n", primary)
		if err := tcpServer.ListenAndServe(); err != nil {
			log.Printf("[DNS] TCP server error: %v", err)
		}
	}()

	for _, a := range addrs[1:] {
		bind := net.JoinHostPort(a, listenPort)
		startExtraDNSListener(bind, "udp", ddnsMsgAcceptFunc, tsigSecrets)
		startExtraDNSListener(bind, "tcp", ddnsMsgAcceptFunc, tsigSecrets)
	}

	server = &dns.Server{
		Addr:          primary,
		Net:           "udp",
		MsgAcceptFunc: ddnsMsgAcceptFunc,
		TsigSecret:    tsigSecrets,
		Handler:       dns.HandlerFunc(handleDNSRequest),
	}

	fmt.Printf("DNS forwarder listening on %s (UDP). Local IP: %s %s. Upstream IPv4: %s IPv6: %s\n",
		primary, localIp, localIPv6, currentResolver(), currentResolverIPv6())
	err := server.ListenAndServe()
	if err != nil {
		fmt.Println(err)
		// os.Exit(1)
		return
	}

}

func StopDNSServer() {
	if server == nil || !serverRunning.Load() {
		fmt.Println("DNS server is already stopped")
		return
	}

	// Persist device store to disk before shutdown
	if deviceStore != nil {
		if err := deviceStore.SaveNow(); err != nil {
			log.Printf("[DNS] Error persisting device store: %v", err)
		}
	}

	if cacheRecorder != nil {
		cacheRecorder.Stop()
		cacheRecorder = nil
	}

	// Stop DNS response cache (stops reaper goroutine)
	if dnsResponseCache != nil {
		dnsResponseCache.Stop()
		dnsResponseCache = nil
	}

	// Stop mDNS browser if running
	if mdnsBrowser != nil {
		mdnsBrowser.Stop()
		mdnsBrowser = nil
	}

	// Stop TCP server if running
	if tcpServer != nil {
		if err := tcpServer.Shutdown(); err != nil {
			log.Printf("[DNS] Error shutting down TCP server: %v", err)
		}
		tcpServer = nil
	}

	if server != nil {
		if err := server.Shutdown(); err != nil {
			log.Printf("[DNS] Error shutting down UDP server: %v", err)
		}
		server = nil
	}

	for _, s := range extraDNSServers {
		if s != nil {
			_ = s.Shutdown()
		}
	}
	extraDNSServers = nil

	serverRunning.Store(false)
}

func dnsListenAddrs() []string {
	parts := strings.Split(listenAddr, ",")
	var addrs []string
	seen := map[string]bool{}
	hasV4, hasV6 := false, false
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		addrs = append(addrs, p)
		ip := net.ParseIP(p)
		if p == "0.0.0.0" || (ip != nil && ip.To4() != nil) {
			hasV4 = true
		} else {
			hasV6 = true
		}
	}
	if len(addrs) == 0 {
		addrs = []string{"0.0.0.0"}
		hasV4 = true
	}
	if hasV4 && !hasV6 {
		addrs = append(addrs, "::")
	}
	return addrs
}

func startExtraDNSListener(bind, network string, accept dns.MsgAcceptFunc, tsig map[string]string) {
	s := &dns.Server{
		Addr:          bind,
		Net:           network,
		MsgAcceptFunc: accept,
		TsigSecret:    tsig,
		Handler:       dns.HandlerFunc(handleDNSRequest),
	}
	extraDNSServers = append(extraDNSServers, s)
	go func() {
		log.Printf("[DNS] extra listener %s %s", network, bind)
		if err := s.ListenAndServe(); err != nil {
			log.Printf("[DNS] extra %s %s: %v", network, bind, err)
		}
	}()
}

// getDomainListIDs reads a JSON array of domain list IDs from the given
// settings key. Returns nil if the key is empty or invalid.
func getDomainListIDs(key string) []string {
	if dnsSettings == nil {
		return nil
	}
	raw := dnsSettings.Get(key)
	if raw == "" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		log.Printf("[DNS] Error parsing %s: %v", key, err)
		return nil
	}
	return ids
}

// RefreshDNSListIDs re-parses dns_domain_lists / whitelist IDs from settings.
// Call after those settings change and at DNS server start.
func RefreshDNSListIDs() {
	cachedBlockListIDs.Store(getDomainListIDs("dns_domain_lists"))
	cachedAllowListIDs.Store(getDomainListIDs("dns_whitelist_domain_lists"))
}

func loadCachedListIDs(v atomic.Value) []string {
	ids, _ := v.Load().([]string)
	return ids
}

// RegisterApplianceNames rebuilds local A records for this host.
func RegisterApplianceNames() {
	registerApplianceNames(dnsSettings)
}

func registerApplianceNames(settings *gatesentry2storage.MapStore) {
	names := make(map[string]bool)
	add := func(n string) {
		n = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(n, ".")))
		if n != "" {
			names[n] = true
		}
	}

	if hostname, err := os.Hostname(); err == nil {
		add(hostname)
		add(hostname + ".local")
		zoneSetting := ""
		if settings != nil {
			zoneSetting = settings.Get("dns_local_zone")
		}
		for _, z := range parseDNSZones(zoneSetting) {
			add(hostname + "." + z)
		}
	}
	if env := os.Getenv("GS_ADMIN_HOSTS"); env != "" {
		for _, n := range strings.Split(env, ",") {
			add(n)
		}
	}
	if settings != nil {
		add(settings.Get("wpad_proxy_host"))
	}

	selfNamesMu.Lock()
	selfNames = names
	selfNamesMu.Unlock()

	if ip, err := gatesentryDnsUtils.GetLocalIP(); err == nil {
		localIp = ip
	}
	if ip, err := gatesentryDnsUtils.GetLocalIPv6(); err == nil {
		localIPv6 = ip
	}
	localIps = gatesentryDnsUtils.GetLocalIPs()
	localIPv6s = gatesentryDnsUtils.GetLocalIPv6s()
	if localIp != "" {
		mutex.Lock()
		for n := range names {
			internalRecords[n] = localIp
		}
		mutex.Unlock()
	}
	log.Printf("[DNS] Appliance names: %v → A %v AAAA %v", mapKeys(names), localIps, localIPv6s)
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func isApplianceName(domain string) bool {
	selfNamesMu.RLock()
	ok := selfNames[strings.ToLower(domain)]
	selfNamesMu.RUnlock()
	return ok
}

func selfAnswers(qname string, qtype uint16) []dns.RR {
	// Re-scan each time so plugging in a NIC is picked up without restart.
	localIps = gatesentryDnsUtils.GetLocalIPs()
	localIPv6s = gatesentryDnsUtils.GetLocalIPv6s()
	if len(localIps) > 0 {
		localIp = localIps[0]
	}
	if len(localIPv6s) > 0 {
		localIPv6 = localIPv6s[0]
	}

	var out []dns.RR
	wantA := qtype == dns.TypeA || qtype == dns.TypeANY
	wantAAAA := qtype == dns.TypeAAAA || qtype == dns.TypeANY
	if wantA {
		ips := localIps
		if len(ips) == 0 && localIp != "" {
			ips = []string{localIp}
		}
		for _, s := range ips {
			if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
				out = append(out, &dns.A{
					Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
					A:   ip,
				})
			}
		}
	}
	if wantAAAA {
		ips := localIPv6s
		if len(ips) == 0 && localIPv6 != "" {
			ips = []string{localIPv6}
		}
		for _, s := range ips {
			if ip := net.ParseIP(s); ip != nil && ip.To4() == nil {
				out = append(out, &dns.AAAA{
					Hdr:  dns.RR_Header{Name: qname, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
					AAAA: ip,
				})
			}
		}
	}
	return out
}

// isDomainBlocked checks whether a domain should be blocked by DNS filtering.
// A domain is blocked when:
//  1. It appears in ANY domain list referenced by dns_domain_lists, AND
//  2. It does NOT appear in ANY domain list referenced by dns_whitelist_domain_lists.
//
// Uses the shared DomainListIndex for O(1) lookups. Falls back to the legacy
// blockedDomains map if no DomainListManager is available (graceful degradation).
func isDomainBlocked(domain string) bool {
	if domainListMgr == nil || domainListMgr.Index == nil {
		// Fallback to legacy map for backwards compatibility
		mutex.RLock()
		blocked := blockedDomains[domain]
		mutex.RUnlock()
		return blocked
	}

	blockListIDs := loadCachedListIDs(cachedBlockListIDs)
	if len(blockListIDs) == 0 {
		return false
	}
	if !domainListMgr.Index.IsDomainInAnyList(domain, blockListIDs) {
		return false
	}

	whitelistIDs := loadCachedListIDs(cachedAllowListIDs)
	if len(whitelistIDs) > 0 && domainListMgr.Index.IsDomainInAnyList(domain, whitelistIDs) {
		return false
	}

	return true
}

func handleDNSRequest(w dns.ResponseWriter, r *dns.Msg) {
	queryStart := time.Now()
	dnsMetrics.QueriesTotal.Add(1)
	defer func() { dnsMetrics.QueryDuration.Observe(time.Since(queryStart)) }()

	// Check if server is running (atomic read - no lock needed)
	if !serverRunning.Load() {
		log.Println("DNS server is not running")
		w.Close()
		return
	}

	// Route DDNS UPDATE messages to the dedicated handler.
	// UPDATE messages have a different structure (zone section, update section)
	// and are handled entirely separately from standard queries.
	if r.Opcode == dns.OpcodeUpdate {
		dnsMetrics.QueriesDDNS.Add(1)
		handleDDNSUpdate(w, r)
		return
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	// Passive discovery: record that we saw a query from this client IP.
	// Runs in a goroutine to avoid adding latency to DNS responses.
	// The device store handles deduplication and MAC correlation internally.
	if deviceStore != nil {
		clientIP := discovery.ExtractClientIP(w.RemoteAddr())
		if clientIP != "" {
			go deviceStore.ObservePassiveQuery(clientIP)
		}
	}

	for _, q := range r.Question {
		domain := strings.ToLower(strings.TrimSuffix(q.Name, "."))
		if domain == "" {
			continue
		}

		// This host (all NICs) before the device store, which may only know one IP.
		if isApplianceName(domain) {
			if answers := selfAnswers(q.Name, q.Qtype); len(answers) > 0 {
				response := new(dns.Msg)
				response.SetRcode(r, dns.RcodeSuccess)
				response.Authoritative = true
				response.Answer = answers
				logger.LogDNS(domain, "dns", "self")
				emitRequestEvent(domain, dns.TypeToString[q.Qtype], "self", false)
				w.WriteMsg(response)
				return
			}
		}

		// --- 1. Device store lookup (supports A, AAAA, PTR) ---
		// The device store has its own RWMutex — no need to hold the shared mutex.
		if deviceStore != nil {
			var records []discovery.DnsRecord

			// PTR queries: check reverse lookup index
			if q.Qtype == dns.TypePTR && isReverseDomain(domain) {
				records = deviceStore.LookupReverse(domain)
			} else {
				// Forward queries: A, AAAA, or ANY
				records = deviceStore.LookupName(domain, q.Qtype)
			}

			if len(records) > 0 {
				dnsMetrics.QueriesDevice.Add(1)
				log.Printf("[DNS] Device store hit: %s %s (%d records)",
					domain, dns.TypeToString[q.Qtype], len(records))
				response := new(dns.Msg)
				response.SetRcode(r, dns.RcodeSuccess)
				response.Authoritative = true
				for _, rec := range records {
					rr := rec.ToRR()
					if rr != nil {
						response.Answer = append(response.Answer, rr)
					}
				}
				logger.LogDNS(domain, "dns", "device")
				emitRequestEvent(domain, dns.TypeToString[q.Qtype], "device", false)
				w.WriteMsg(response)
				return
			}
		}

		// --- WPAD DNS interception ---
		// If the queried domain starts with "wpad." or is exactly "wpad",
		// return our own IP so the client fetches the PAC file from us.
		// This enables automatic proxy configuration for all network clients
		// without requiring DHCP option 252.
		if wpadEnabled.Load() {
			lowerDomain := strings.ToLower(domain)
			if lowerDomain == "wpad" || strings.HasPrefix(lowerDomain, "wpad.") {
				if answers := selfAnswers(q.Name, q.Qtype); len(answers) > 0 {
					dnsMetrics.QueriesWPAD.Add(1)
					response := new(dns.Msg)
					response.SetRcode(r, dns.RcodeSuccess)
					response.Authoritative = true
					response.Answer = answers
					logger.LogDNS(domain, "dns", "wpad")
					emitRequestEvent(domain, dns.TypeToString[q.Qtype], "wpad", false)
					w.WriteMsg(response)
					return
				}
			}
		}

		// --- 2. Exception / internal / blocked ---
		// Internal records and exception domains still use the legacy maps
		// protected by the shared mutex.
		mutex.RLock()
		isException := exceptionDomains[domain]
		internalIP, isInternal := internalRecords[domain]
		mutex.RUnlock()

		// Blocked-domain check now uses the shared DomainListIndex (O(1) lookup).
		// The index has its own RWMutex — no need to hold the legacy mutex.
		isBlocked := isDomainBlocked(domain)

		if isException {
			dnsMetrics.QueriesException.Add(1)
			log.Println("Domain is exception : ", domain)
			logger.LogDNS(domain, "dns", "exception")
			emitRequestEvent(domain, dns.TypeToString[q.Qtype], "exception", false)

		} else if isInternal {
			dnsMetrics.QueriesInternal.Add(1)
			log.Println("Domain is internal : ", domain, " - ", internalIP)
			response := new(dns.Msg)
			response.SetRcode(r, dns.RcodeSuccess)
			response.Answer = append(response.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP(internalIP),
			})
			logger.LogDNS(domain, "dns", "internal")
			emitRequestEvent(domain, dns.TypeToString[q.Qtype], "internal", false)
			w.WriteMsg(response)
			return
		} else if isBlocked && dnsFilteringEnabled.Load() {
			dnsMetrics.QueriesBlocked.Add(1)
			response := new(dns.Msg)
			response.SetRcode(r, dns.RcodeSuccess)
			response.Answer = selfAnswers(domain+".", q.Qtype)
			if len(response.Answer) == 0 && localIp != "" {
				response.Answer = append(response.Answer, &dns.A{
					Hdr: dns.RR_Header{Name: domain + ".", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
					A:   net.ParseIP(localIp),
				})
			}
			logger.LogDNS(domain, "dns", "blocked")
			emitRequestEvent(domain, dns.TypeToString[q.Qtype], "blocked", true)
			w.WriteMsg(response)
			return
		} else {
			// Will be logged as "forward" or "cached" below after the cache check
		}

		// --- 3. Forward to external resolver (with cache) ---
		// Check cache first — avoids upstream round-trip for repeated queries.
		if dnsResponseCache != nil {
			if cached := dnsResponseCache.Get(q.Name, q.Qtype); cached != nil {
				dnsMetrics.QueriesCached.Add(1)
				cached.SetReply(r)
				cached.Authoritative = false
				logger.LogDNS(domain, "dns", "cached")
				emitRequestEvent(domain, dns.TypeToString[q.Qtype], "cached", false)
				w.WriteMsg(cached)
				return
			}
		}

		// Cache miss — forward to external resolver (no mutex held).
		useTCP := w.LocalAddr().Network() == "tcp"
		resp, err := forwardDNSRequest(r, useTCP)
		if err != nil {
			dnsMetrics.QueriesError.Add(1)
			if dnsResponseCache != nil {
				dnsResponseCache.PutFailure(q.Name, q.Qtype, dns.RcodeServerFailure)
			}
			logger.LogDNS(domain, "dns", "error")
			errMsg := new(dns.Msg)
			errMsg.SetRcode(r, dns.RcodeServerFailure)
			emitRequestEvent(domain, dns.TypeToString[q.Qtype], "error", false)
			w.WriteMsg(errMsg)
			return
		}

		logger.LogDNS(domain, "dns", "forward")

		// Cache the upstream response for future queries.
		if dnsResponseCache != nil {
			dnsResponseCache.Put(q.Name, q.Qtype, resp)
		}
		dnsMetrics.QueriesForwarded.Add(1)
		emitRequestEvent(domain, dns.TypeToString[q.Qtype], "forwarded", false)

		// Phase 4: Propagate the upstream rcode (NXDOMAIN, NOERROR, etc.)
		// and copy answers + authority section (contains SOA for negative responses).
		m.Rcode = resp.Rcode
		m.Answer = append(m.Answer, resp.Answer...)
		m.Ns = append(m.Ns, resp.Ns...)
	}
	w.WriteMsg(m)
}

// isReverseDomain returns true if the domain is a PTR reverse-lookup name.
func isReverseDomain(domain string) bool {
	return strings.HasSuffix(domain, ".in-addr.arpa") ||
		strings.HasSuffix(domain, ".ip6.arpa")
}

func forwardDNSRequest(r *dns.Msg, useTCP bool) (*dns.Msg, error) {
	if circuitIsOpen() {
		return nil, fmt.Errorf("upstream circuit open")
	}
	if !tryAcquireForward() {
		return nil, fmt.Errorf("too many in-flight upstream queries")
	}
	defer releaseForward()

	c := new(dns.Client)
	c.Timeout = 3 * time.Second

	if useTCP {
		c.Net = "tcp"
	}

	upstream := resolverForMsg(r)
	resp, rtt, err := c.Exchange(r, upstream)
	if rtt > 0 {
		dnsMetrics.UpstreamDuration.Observe(rtt)
	}
	if err != nil {
		noteUpstreamFailure()
		return nil, err
	}
	noteUpstreamSuccess()

	if resp.Truncated && !useTCP {
		c.Net = "tcp"
		tcpResp, tcpRTT, tcpErr := c.Exchange(r, upstream)
		if tcpRTT > 0 {
			dnsMetrics.UpstreamDuration.Observe(tcpRTT)
		}
		if tcpErr != nil {
			// TCP retry failed, return the truncated UDP response
			log.Println("[DNS] TCP retry failed:", tcpErr)
			return resp, nil
		}
		return tcpResp, nil
	}

	return resp, nil
}

// function that accepts two strings : domain and ip and returns an A record
func GetARecord(domain string, ip string) *dns.A {
	return &dns.A{
		Hdr: dns.RR_Header{Name: domain + ".", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 3600},
		A:   net.ParseIP(ip),
	}
}

// function that accepts two strings : domain and ip and returns a TXT record
func GetTXTRecord(domain string, txt string) *dns.TXT {
	return &dns.TXT{
		Hdr: dns.RR_Header{Name: domain + ".", Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 3600},
		Txt: []string{txt},
	}
}

// function that accepts two strings : domain and ip and returns a CNAME record
func GetCNAMERecord(domain string, cname string) *dns.CNAME {
	return &dns.CNAME{
		Hdr:    dns.RR_Header{Name: domain + ".", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 3600},
		Target: cname + ".",
	}
}
