package diagnostic

import "time"

type ipContextCacheEntry struct {
	value   IPContext
	expires time.Time
	used    uint64
}

func contextSnapshotTTL(v IPContext) time.Duration {
	stable := contextMember(v.ReverseDNS.Status, "ok", "limited", "not_found") && contextMember(v.Registration.Status, "ok", "not_found") && contextMember(v.Routing.Status, "ok", "limited", "not_found")
	for _, name := range v.ReverseDNS.Names {
		stable = stable && contextMember(name.ForwardStatus, "confirmed", "mismatch", "not_found", "limited")
	}
	for _, origin := range v.Routing.Origins {
		stable = stable && origin.RPKI.Status == "ok"
	}
	if stable {
		return 5 * time.Minute
	}
	return 30 * time.Second
}

// Called only under the owner's mutex; expiry is lazy, no per-entry goroutines.
func (s *IPContextService) cacheSnapshot(value IPContext) {
	if len(s.cache) >= 256 {
		key := ""
		var oldest uint64
		for address, entry := range s.cache {
			if key == "" || entry.used < oldest || (entry.used == oldest && address < key) {
				key, oldest = address, entry.used
			}
		}
		delete(s.cache, key)
	}
	s.sequence++
	expires, _ := time.Parse("2006-01-02T15:04:05.000Z", value.ExpiresAt)
	s.cache[value.Address] = ipContextCacheEntry{cloneIPContext(value), expires, s.sequence}
}
