package main

import "time"

const configCacheTTL = 2 * time.Second

type configCacheEntry struct {
	Value     string
	ExpiresAt time.Time
}

func (s *server) getCachedConfig(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	now := time.Now()
	s.configCacheMu.RLock()
	entry, ok := s.configCache[key]
	s.configCacheMu.RUnlock()
	if !ok || !now.Before(entry.ExpiresAt) {
		if ok {
			s.configCacheMu.Lock()
			delete(s.configCache, key)
			s.configCacheMu.Unlock()
		}
		return "", false
	}
	return entry.Value, true
}

func (s *server) storeCachedConfig(key, value string) {
	if s == nil {
		return
	}
	s.configCacheMu.Lock()
	if s.configCache == nil {
		s.configCache = make(map[string]configCacheEntry)
	}
	if len(s.configCache) > 64 {
		now := time.Now()
		for k, v := range s.configCache {
			if !now.Before(v.ExpiresAt) {
				delete(s.configCache, k)
			}
		}
		for k := range s.configCache {
			if len(s.configCache) <= 64 {
				break
			}
			delete(s.configCache, k)
		}
	}
	s.configCache[key] = configCacheEntry{Value: value, ExpiresAt: time.Now().Add(configCacheTTL)}
	s.configCacheMu.Unlock()
}

func (s *server) clearConfigCache() {
	if s == nil {
		return
	}
	s.configCacheMu.Lock()
	s.configCache = make(map[string]configCacheEntry)
	s.configCacheMu.Unlock()
}
