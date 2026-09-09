package gatesentryDnsFilter

import (
	"encoding/json"
	"log"
	"strings"
	"sync"

	gatesentry2storage "bitbucket.org/abdullah_irfan/gatesentryf/storage"
	gatesentryTypes "bitbucket.org/abdullah_irfan/gatesentryf/types"
)

// OnCustomRecordsLoaded publishes the loaded A/AAAA maps to the DNS query
// path. Set by the DNS server to avoid an import cycle.
var OnCustomRecordsLoaded func(ipv4, ipv6 map[string]string)

func InitializeInternalRecords(records *map[string]string, aaaa *map[string]string, mutex *sync.RWMutex, settings *gatesentry2storage.MapStore) {
	mutex.Lock()
	defer mutex.Unlock()
	internalRecordsString := settings.Get("DNS_custom_entries")
	var customEntries []gatesentryTypes.DNSCustomEntry
	json.Unmarshal([]byte(internalRecordsString), &customEntries)

	*records = make(map[string]string)
	if aaaa != nil {
		*aaaa = make(map[string]string)
	}
	for _, entry := range customEntries {
		domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(entry.Domain), "."))
		if domain == "" {
			continue
		}
		if entry.IP != "" {
			(*records)[domain] = entry.IP
		}
		if aaaa != nil && entry.IPv6 != "" {
			(*aaaa)[domain] = entry.IPv6
		}
	}

	aaaaMap := map[string]string{}
	if aaaa != nil {
		aaaaMap = *aaaa
	}
	if OnCustomRecordsLoaded != nil {
		OnCustomRecordsLoaded(*records, aaaaMap)
	}
	log.Printf("[DNS] Custom records loaded: %d A, %d AAAA", len(*records), len(aaaaMap))
}
