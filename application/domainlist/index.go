package domainlist

import (
	"strings"
	"sync"
)

// indexShards keeps a list rebuild from holding every lookup.
// A Test Domain check locks only the shard for that name. A list refresh
// updates one shard at a time, so a lookup waits for one shard, not for
// the whole multi-million-name list to be inserted.
const indexShards = 64

// parentDomains returns the domain and its parents down to 2 labels
// (example.com), skipping the public TLD (com).
func parentDomains(domain string) []string {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return nil
	}
	out := []string{domain}
	for {
		i := strings.IndexByte(domain, '.')
		if i < 0 {
			break
		}
		domain = domain[i+1:]
		if !strings.Contains(domain, ".") {
			break
		}
		out = append(out, domain)
	}
	return out
}

func shardIndex(domain string) uint {
	var h uint32 = 2166136261
	for i := 0; i < len(domain); i++ {
		h ^= uint32(domain[i])
		h *= 16777619
	}
	return uint(h % indexShards)
}

type indexShard struct {
	mu      sync.RWMutex
	domains map[string]map[string]bool
	byList  map[string]map[string]struct{}
}

func (s *indexShard) ensureLocked() {
	if s.domains == nil {
		s.domains = make(map[string]map[string]bool)
	}
	if s.byList == nil {
		s.byList = make(map[string]map[string]struct{})
	}
}

// DomainListIndex provides O(1) domain lookup across all loaded domain lists.
// It maps each domain to the set of list IDs that contain it.
//
// The index is split into shards so a lookup does not wait for another list
// to finish loading.
type DomainListIndex struct {
	shards [indexShards]indexShard
}

// NewDomainListIndex creates an empty index.
func NewDomainListIndex() *DomainListIndex {
	return &DomainListIndex{}
}

// IsDomainInList checks whether a domain is present in a specific list.
// O(1) lookup. Thread-safe for concurrent reads.
func (idx *DomainListIndex) IsDomainInList(domain string, listID string) bool {
	if idx == nil {
		return false
	}
	for _, d := range parentDomains(domain) {
		s := &idx.shards[shardIndex(d)]
		s.mu.RLock()
		lists := s.domains[d]
		found := lists != nil && lists[listID]
		s.mu.RUnlock()
		if found {
			return true
		}
	}
	return false
}

// IsDomainInAnyList checks whether a domain (or a parent zone) is present in
// ANY of the given lists. Returns true on first match.
func (idx *DomainListIndex) IsDomainInAnyList(domain string, listIDs []string) bool {
	if idx == nil || len(listIDs) == 0 {
		return false
	}
	for _, d := range parentDomains(domain) {
		s := &idx.shards[shardIndex(d)]
		s.mu.RLock()
		lists := s.domains[d]
		found := false
		if lists != nil {
			for _, id := range listIDs {
				if lists[id] {
					found = true
					break
				}
			}
		}
		s.mu.RUnlock()
		if found {
			return true
		}
	}
	return false
}

// GetListsForDomain returns the set of list IDs that contain the given domain.
// Returns nil if the domain is not indexed. Thread-safe.
func (idx *DomainListIndex) GetListsForDomain(domain string) map[string]bool {
	if idx == nil {
		return nil
	}
	s := &idx.shards[shardIndex(domain)]
	s.mu.RLock()
	defer s.mu.RUnlock()

	lists := s.domains[domain]
	if lists == nil {
		return nil
	}
	result := make(map[string]bool, len(lists))
	for k, v := range lists {
		result[k] = v
	}
	return result
}

// AddDomains adds a slice of domains to the index under the given list ID.
// Used during initial load and refresh.
func (idx *DomainListIndex) AddDomains(listID string, domains []string) {
	if idx == nil {
		return
	}
	grouped := groupByShard(domains)
	for i := range idx.shards {
		fresh := grouped[i]
		if len(fresh) == 0 {
			continue
		}
		s := &idx.shards[i]
		s.mu.Lock()
		s.ensureLocked()
		known := s.byList[listID]
		if known == nil {
			known = make(map[string]struct{})
			s.byList[listID] = known
		}
		for domain := range fresh {
			if s.domains[domain] == nil {
				s.domains[domain] = make(map[string]bool)
			}
			s.domains[domain][listID] = true
			known[domain] = struct{}{}
		}
		s.mu.Unlock()
	}
}

// RemoveList removes all entries for a specific list ID from the index.
// This is used when a domain list is deleted or before it is reloaded.
func (idx *DomainListIndex) RemoveList(listID string) {
	if idx == nil {
		return
	}
	for i := range idx.shards {
		s := &idx.shards[i]
		s.mu.Lock()
		if s.byList != nil {
			for domain := range s.byList[listID] {
				lists := s.domains[domain]
				delete(lists, listID)
				if len(lists) == 0 {
					delete(s.domains, domain)
				}
			}
			delete(s.byList, listID)
		}
		s.mu.Unlock()
	}
}

// ReplaceList removes all domains for a list and adds new ones.
// Shards are updated one at a time so a lookup is not blocked for the
// whole list. A name that is in both the old and new set stays indexed
// for the whole call.
func (idx *DomainListIndex) ReplaceList(listID string, domains []string) {
	if idx == nil {
		return
	}
	grouped := groupByShard(domains)
	for i := range idx.shards {
		s := &idx.shards[i]
		fresh := grouped[i]
		s.mu.Lock()
		s.ensureLocked()
		old := s.byList[listID]
		for domain := range old {
			if _, keep := fresh[domain]; keep {
				continue
			}
			lists := s.domains[domain]
			delete(lists, listID)
			if len(lists) == 0 {
				delete(s.domains, domain)
			}
		}
		if len(fresh) == 0 {
			delete(s.byList, listID)
			s.mu.Unlock()
			continue
		}
		for domain := range fresh {
			if _, already := old[domain]; already {
				continue
			}
			if s.domains[domain] == nil {
				s.domains[domain] = make(map[string]bool)
			}
			s.domains[domain][listID] = true
		}
		s.byList[listID] = fresh
		s.mu.Unlock()
	}
}

// TotalDomains returns the total number of unique domains in the index.
func (idx *DomainListIndex) TotalDomains() int {
	if idx == nil {
		return 0
	}
	total := 0
	for i := range idx.shards {
		s := &idx.shards[i]
		s.mu.RLock()
		total += len(s.domains)
		s.mu.RUnlock()
	}
	return total
}

// CountForList returns the number of domains associated with a specific list ID.
func (idx *DomainListIndex) CountForList(listID string) int {
	if idx == nil {
		return 0
	}
	count := 0
	for i := range idx.shards {
		s := &idx.shards[i]
		s.mu.RLock()
		count += len(s.byList[listID])
		s.mu.RUnlock()
	}
	return count
}

// CountForLists returns the number of unique domains that belong to any of
// the given list IDs. Domains that appear only on other lists are excluded.
// An empty selection returns 0.
func (idx *DomainListIndex) CountForLists(listIDs []string) int {
	if idx == nil || len(listIDs) == 0 {
		return 0
	}
	want := make(map[string]struct{}, len(listIDs))
	for _, id := range listIDs {
		if id != "" {
			want[id] = struct{}{}
		}
	}
	if len(want) == 0 {
		return 0
	}

	count := 0
	for i := range idx.shards {
		s := &idx.shards[i]
		s.mu.RLock()
		for _, lists := range s.domains {
			for id := range lists {
				if _, ok := want[id]; ok {
					count++
					break
				}
			}
		}
		s.mu.RUnlock()
	}
	return count
}

// Clear removes all entries from the index.
func (idx *DomainListIndex) Clear() {
	if idx == nil {
		return
	}
	for i := range idx.shards {
		s := &idx.shards[i]
		s.mu.Lock()
		s.domains = make(map[string]map[string]bool)
		s.byList = make(map[string]map[string]struct{})
		s.mu.Unlock()
	}
}

func groupByShard(domains []string) []map[string]struct{} {
	grouped := make([]map[string]struct{}, indexShards)
	for _, domain := range domains {
		if domain == "" {
			continue
		}
		n := shardIndex(domain)
		if grouped[n] == nil {
			grouped[n] = make(map[string]struct{})
		}
		grouped[n][domain] = struct{}{}
	}
	return grouped
}
