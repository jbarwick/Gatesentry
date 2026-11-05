package domainlist

import (
	"strings"
	"sync"
	"sync/atomic"
)

// indexShards spreads names so a list refresh copies one slice at a time.
// Lookups never take that lock. They read the snapshot already published.
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
	for i := range len(domain) {
		h ^= uint32(domain[i])
		h *= 16777619
	}
	return uint(h % indexShards)
}

// shardSnap is immutable once published. Writers clone it and store a new
// pointer. Readers load the pointer and do not lock, so a DNS query does
// not wait while a domain list is rebuilt.
type shardSnap struct {
	domains map[string]map[string]bool
	byList  map[string]map[string]struct{}
}

var emptySnap = &shardSnap{
	domains: map[string]map[string]bool{},
	byList:  map[string]map[string]struct{}{},
}

type indexShard struct {
	mu   sync.Mutex // writers only
	snap atomic.Pointer[shardSnap]
}

func (s *indexShard) load() *shardSnap {
	p := s.snap.Load()
	if p == nil {
		return emptySnap
	}
	return p
}

func (s *indexShard) publish(next *shardSnap) {
	s.snap.Store(next)
}

func cloneSnap(old *shardSnap) *shardSnap {
	if old == nil {
		old = emptySnap
	}
	next := &shardSnap{
		domains: make(map[string]map[string]bool, len(old.domains)),
		byList:  make(map[string]map[string]struct{}, len(old.byList)),
	}
	for domain, lists := range old.domains {
		next.domains[domain] = lists
	}
	for id, set := range old.byList {
		next.byList[id] = set
	}
	return next
}

func cloneListSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneDomainSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in)+1)
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

// DomainListIndex provides O(1) domain lookup across all loaded domain lists.
// It maps each domain to the set of list IDs that contain it.
//
// Each shard publishes an immutable snapshot. A lookup loads that snapshot
// and does not wait for a list refresh.
type DomainListIndex struct {
	shards [indexShards]indexShard
}

// NewDomainListIndex creates an empty index.
func NewDomainListIndex() *DomainListIndex {
	return &DomainListIndex{}
}

// IsDomainInList checks whether a domain is present in a specific list.
// O(1) lookup. Does not wait on a list refresh.
func (idx *DomainListIndex) IsDomainInList(domain string, listID string) bool {
	if idx == nil {
		return false
	}
	for _, d := range parentDomains(domain) {
		lists := idx.shards[shardIndex(d)].load().domains[d]
		if lists != nil && lists[listID] {
			return true
		}
	}
	return false
}

// IsDomainInAnyList checks whether a domain (or a parent zone) is present in
// ANY of the given lists. Returns true on first match.
// Does not wait on a list refresh.
func (idx *DomainListIndex) IsDomainInAnyList(domain string, listIDs []string) bool {
	if idx == nil || len(listIDs) == 0 {
		return false
	}
	for _, d := range parentDomains(domain) {
		lists := idx.shards[shardIndex(d)].load().domains[d]
		if lists == nil {
			continue
		}
		for _, id := range listIDs {
			if lists[id] {
				return true
			}
		}
	}
	return false
}

// GetListsForDomain returns the set of list IDs that contain the given domain.
// Returns nil if the domain is not indexed. Does not wait on a list refresh.
func (idx *DomainListIndex) GetListsForDomain(domain string) map[string]bool {
	if idx == nil {
		return nil
	}
	lists := idx.shards[shardIndex(domain)].load().domains[domain]
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
		next := cloneSnap(s.load())
		known := cloneDomainSet(next.byList[listID])
		for domain := range fresh {
			lists := next.domains[domain]
			if lists[listID] {
				known[domain] = struct{}{}
				continue
			}
			copied := cloneListSet(lists)
			copied[listID] = true
			next.domains[domain] = copied
			known[domain] = struct{}{}
		}
		next.byList[listID] = known
		s.publish(next)
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
		old := s.load()
		if _, ok := old.byList[listID]; !ok {
			s.mu.Unlock()
			continue
		}
		next := cloneSnap(old)
		for domain := range next.byList[listID] {
			lists := next.domains[domain]
			if len(lists) == 1 && lists[listID] {
				delete(next.domains, domain)
				continue
			}
			copied := cloneListSet(lists)
			delete(copied, listID)
			if len(copied) == 0 {
				delete(next.domains, domain)
			} else {
				next.domains[domain] = copied
			}
		}
		delete(next.byList, listID)
		s.publish(next)
		s.mu.Unlock()
	}
}

// ReplaceList removes all domains for a list and adds new ones.
// Each shard is published on its own. A name that is in both the old and
// new set stays in the snapshot readers already have, and in the new one.
// Lookups do not wait for this call.
func (idx *DomainListIndex) ReplaceList(listID string, domains []string) {
	if idx == nil {
		return
	}
	grouped := groupByShard(domains)
	for i := range idx.shards {
		s := &idx.shards[i]
		fresh := grouped[i]
		s.mu.Lock()
		old := s.load()
		prev := old.byList[listID]
		if len(prev) == 0 && len(fresh) == 0 {
			s.mu.Unlock()
			continue
		}
		next := cloneSnap(old)
		for domain := range prev {
			if _, keep := fresh[domain]; keep {
				continue
			}
			lists := next.domains[domain]
			if len(lists) == 1 && lists[listID] {
				delete(next.domains, domain)
				continue
			}
			copied := cloneListSet(lists)
			delete(copied, listID)
			if len(copied) == 0 {
				delete(next.domains, domain)
			} else {
				next.domains[domain] = copied
			}
		}
		if len(fresh) == 0 {
			delete(next.byList, listID)
			s.publish(next)
			s.mu.Unlock()
			continue
		}
		for domain := range fresh {
			if _, already := prev[domain]; already {
				continue
			}
			copied := cloneListSet(next.domains[domain])
			copied[listID] = true
			next.domains[domain] = copied
		}
		next.byList[listID] = fresh
		s.publish(next)
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
		total += len(idx.shards[i].load().domains)
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
		count += len(idx.shards[i].load().byList[listID])
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
		for _, lists := range idx.shards[i].load().domains {
			for id := range lists {
				if _, ok := want[id]; ok {
					count++
					break
				}
			}
		}
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
		s.publish(emptySnap)
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
