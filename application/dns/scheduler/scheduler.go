package gatesentryDnsScheduler

import (
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	gatesentryDnsFilter "bitbucket.org/abdullah_irfan/gatesentryf/dns/filter"
	gatesentryDomainList "bitbucket.org/abdullah_irfan/gatesentryf/domainlist"
	gatesentry2storage "bitbucket.org/abdullah_irfan/gatesentryf/storage"
	gatesentryTypes "bitbucket.org/abdullah_irfan/gatesentryf/types"
)

type InitializerType func(*map[string]bool, *[]string, *sync.RWMutex)

func RunScheduler(blockedDomains *map[string]bool,
	blockedLists *[]string,
	internalRecords *map[string]string,
	internalAAAA *map[string]string,
	exceptionDomains *map[string]bool,
	mutex *sync.RWMutex,
	settings *gatesentry2storage.MapStore, dnsinfo *gatesentryTypes.DnsServerInfo,
	updateIntervalHourly int,
	restartChan chan bool,
	dlManager *gatesentryDomainList.DomainListManager,
) {

	ticker := time.NewTicker(time.Duration(updateIntervalHourly) * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-restartChan:
			log.Println("Restarting scheduler...")
			doInitialize(blockedDomains, blockedLists, internalRecords, internalAAAA, exceptionDomains, mutex, settings, dnsinfo, updateIntervalHourly, restartChan, dlManager)
		case <-ticker.C:
			log.Println("Running scheduler...")
			doInitialize(blockedDomains, blockedLists, internalRecords, internalAAAA, exceptionDomains, mutex, settings, dnsinfo, updateIntervalHourly, restartChan, dlManager)
		}
	}

}

func doInitialize(blockedDomains *map[string]bool,
	blockedLists *[]string,
	internalRecords *map[string]string,
	internalAAAA *map[string]string,
	exceptionDomains *map[string]bool,
	mutex *sync.RWMutex,
	settings *gatesentry2storage.MapStore, dnsinfo *gatesentryTypes.DnsServerInfo,
	updateIntervalHourly int,
	restartChan chan bool,
	dlManager *gatesentryDomainList.DomainListManager) {

	// Initialize internal records and exception domains (legacy path — still needed)
	gatesentryDnsFilter.InitializeFilters(internalRecords, internalAAAA, exceptionDomains, mutex, settings)

	// Refresh all domain lists (downloads URL-sourced lists, rebuilds index).
	// This replaces the old InitializeBlockedDomains flow.
	if dlManager != nil {
		log.Println("[DNS Scheduler] Refreshing domain lists via DomainListManager...")
		dlManager.LoadAllLists()
		// Domain Totals counts unique domains on the lists assigned in the
		// Block Lists section (dns_domain_lists). Other stored lists stay
		// in the index for rules and the domain-list catalog.
		ids := blockListIDs(settings)
		dnsinfo.NumberDomainsBlocked = enabledBlockDomainCount(dlManager, ids)
		log.Printf("[DNS Scheduler] Domain list refresh complete. Enabled block-list domains: %d (%d lists)", dnsinfo.NumberDomainsBlocked, len(ids))
	}

	dnsinfo.NextUpdate = int(time.Now().Add(time.Hour * time.Duration(updateIntervalHourly)).Unix())
	dnsinfo.LastUpdated = int(time.Now().Unix())
}

// blockListIDs reads the Block Lists selection. An empty or invalid value
// means no lists are enabled, so the Domain Totals count is zero.
func blockListIDs(settings *gatesentry2storage.MapStore) []string {
	if settings == nil {
		return nil
	}
	raw := strings.TrimSpace(settings.Get("dns_domain_lists"))
	if raw == "" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		log.Printf("[DNS Scheduler] Error parsing dns_domain_lists: %v", err)
		return nil
	}
	return ids
}

func enabledBlockDomainCount(dlManager *gatesentryDomainList.DomainListManager, listIDs []string) int {
	if dlManager == nil || dlManager.Index == nil {
		return 0
	}
	return dlManager.Index.CountForLists(listIDs)
}
