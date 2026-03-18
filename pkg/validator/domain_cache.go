package validator

import (
	"context"
	"sync"
	"time"

	"emailvalidator/pkg/cache"
)

// domainCache represents a cached domain lookup result
type domainCache struct {
	exists          bool
	isCatchAll      bool
	catchAllChecked bool
	timestamp       time.Time
}

// DomainCacheResult is the structure stored in Redis cache
type DomainCacheResult struct {
	Exists          bool `json:"exists"`
	IsCatchAll      bool `json:"is_catch_all"`
	CatchAllChecked bool `json:"catch_all_checked"`
}

// DomainCacheManager handles caching of domain validation results
type DomainCacheManager struct {
	localCache    map[string]domainCache
	cacheMutex    sync.RWMutex
	cacheDuration time.Duration
	redisCache    cache.Cache
}

// NewDomainCacheManager creates a new instance of DomainCacheManager with local cache only
func NewDomainCacheManager(duration time.Duration) *DomainCacheManager {
	return &DomainCacheManager{
		localCache:    make(map[string]domainCache, 100), // Pre-allocate space for better performance
		cacheDuration: duration,
		redisCache:    nil,
	}
}

// NewDomainCacheManagerWithRedis creates a new instance of DomainCacheManager with Redis cache
func NewDomainCacheManagerWithRedis(duration time.Duration, redisCache cache.Cache) *DomainCacheManager {
	return &DomainCacheManager{
		localCache:    make(map[string]domainCache, 100),
		cacheDuration: duration,
		redisCache:    redisCache,
	}
}

// Get retrieves a cached domain validation result
func (m *DomainCacheManager) Get(domain string) (bool, bool) {
	// L1: Check local in-memory cache first (fastest)
	m.cacheMutex.RLock()
	cached, ok := m.localCache[domain]
	if ok && time.Since(cached.timestamp) <= m.cacheDuration {
		m.cacheMutex.RUnlock()
		return cached.exists, true
	}
	m.cacheMutex.RUnlock()

	// L2: Fall back to Redis if available
	if m.redisCache != nil {
		var result DomainCacheResult
		err := m.redisCache.Get(context.Background(), "domain:"+domain, &result)
		if err == nil {
			// Populate L1 cache from L2 hit
			m.cacheMutex.Lock()
			m.localCache[domain] = domainCache{
				exists:          result.Exists,
				isCatchAll:      result.IsCatchAll,
				catchAllChecked: result.CatchAllChecked,
				timestamp:       time.Now(),
			}
			m.cacheMutex.Unlock()
			return result.Exists, true
		}
	}

	return false, false
}

// GetCatchAll retrieves the cached catch-all status for a domain
func (m *DomainCacheManager) GetCatchAll(domain string) (isCatchAll bool, checked bool) {
	// L1: Check local in-memory cache first
	m.cacheMutex.RLock()
	cached, ok := m.localCache[domain]
	if ok && time.Since(cached.timestamp) <= m.cacheDuration {
		m.cacheMutex.RUnlock()
		return cached.isCatchAll, cached.catchAllChecked
	}
	m.cacheMutex.RUnlock()

	// L2: Fall back to Redis if available
	if m.redisCache != nil {
		var result DomainCacheResult
		err := m.redisCache.Get(context.Background(), "domain:"+domain, &result)
		if err == nil {
			// Populate L1 cache from L2 hit
			m.cacheMutex.Lock()
			m.localCache[domain] = domainCache{
				exists:          result.Exists,
				isCatchAll:      result.IsCatchAll,
				catchAllChecked: result.CatchAllChecked,
				timestamp:       time.Now(),
			}
			m.cacheMutex.Unlock()
			return result.IsCatchAll, result.CatchAllChecked
		}
	}

	return false, false
}

// Set stores a domain validation result in both L1 (local) and L2 (Redis) caches
// This method primarily updates the existence check. If catch-all was checked, it might be preserved if we implement read-modify-write,
// but usually existence check happens first. To be safe, we'll preserve other fields if they exist in L1.
func (m *DomainCacheManager) Set(domain string, exists bool) {
	m.cacheMutex.Lock()
	
	// Check if entry already exists to preserve other fields
	var isCatchAll, catchAllChecked bool
	if existing, ok := m.localCache[domain]; ok {
		isCatchAll = existing.isCatchAll
		catchAllChecked = existing.catchAllChecked
	}

	m.localCache[domain] = domainCache{
		exists:          exists,
		isCatchAll:      isCatchAll,
		catchAllChecked: catchAllChecked,
		timestamp:       time.Now(),
	}
	m.cacheMutex.Unlock()

	// L2: Store in Redis if available
	if m.redisCache != nil {
		// We should try to get existing from Redis to preserve fields if not in local cache?
		// For simplicity/performance, we assume local cache is source of truth for immediate updates.
		// If we want full consistency with Redis, we'd need to GET then SET, but that's slow.
		// Given the flow Validate -> CheckCatchAll, we are likely fine.
		result := DomainCacheResult{
			Exists:          exists,
			IsCatchAll:      isCatchAll,
			CatchAllChecked: catchAllChecked,
		}
		_ = m.redisCache.Set(context.Background(), "domain:"+domain, result, m.cacheDuration)
	}
}

// SetCatchAll stores the catch-all status for a domain
func (m *DomainCacheManager) SetCatchAll(domain string, isCatchAll bool) {
	m.cacheMutex.Lock()
	
	// Get existing entry to preserve existence check
	exists := true // Default to true if setting catch-all (implies domain exists)
	if existing, ok := m.localCache[domain]; ok {
		exists = existing.exists
	}

	m.localCache[domain] = domainCache{
		exists:          exists,
		isCatchAll:      isCatchAll,
		catchAllChecked: true,
		timestamp:       time.Now(),
	}
	m.cacheMutex.Unlock()

	// L2: Store in Redis if available
	if m.redisCache != nil {
		result := DomainCacheResult{
			Exists:          exists,
			IsCatchAll:      isCatchAll,
			CatchAllChecked: true,
		}
		_ = m.redisCache.Set(context.Background(), "domain:"+domain, result, m.cacheDuration)
	}
}

// ClearExpired removes expired entries from the local cache
// Note: Redis handles its own TTL expiration
func (m *DomainCacheManager) ClearExpired() {
	m.cacheMutex.Lock()
	now := time.Now()
	for domain, cached := range m.localCache {
		if now.Sub(cached.timestamp) > m.cacheDuration {
			delete(m.localCache, domain)
		}
	}
	m.cacheMutex.Unlock()
}

// SetDuration updates the cache duration
func (m *DomainCacheManager) SetDuration(duration time.Duration) {
	m.cacheMutex.Lock()
	m.cacheDuration = duration
	m.cacheMutex.Unlock()
}

// SetRedisCache sets the Redis cache backend
func (m *DomainCacheManager) SetRedisCache(redisCache cache.Cache) {
	m.redisCache = redisCache
}

// HasRedis returns true if Redis cache is configured
func (m *DomainCacheManager) HasRedis() bool {
	return m.redisCache != nil
}

// Close closes the Redis connection if available
func (m *DomainCacheManager) Close() error {
	if m.redisCache != nil {
		return m.redisCache.Close()
	}
	return nil
}
