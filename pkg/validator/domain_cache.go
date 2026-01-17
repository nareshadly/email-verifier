package validator

import (
	"context"
	"sync"
	"time"

	"emailvalidator/pkg/cache"
)

// domainCache represents a cached domain lookup result
type domainCache struct {
	exists    bool
	timestamp time.Time
}

// DomainCacheResult is the structure stored in Redis cache
type DomainCacheResult struct {
	Exists bool `json:"exists"`
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
				exists:    result.Exists,
				timestamp: time.Now(),
			}
			m.cacheMutex.Unlock()
			return result.Exists, true
		}
	}

	return false, false
}

// Set stores a domain validation result in both L1 (local) and L2 (Redis) caches
func (m *DomainCacheManager) Set(domain string, exists bool) {
	// L1: Store in local in-memory cache
	m.cacheMutex.Lock()
	m.localCache[domain] = domainCache{
		exists:    exists,
		timestamp: time.Now(),
	}
	m.cacheMutex.Unlock()

	// L2: Store in Redis if available
	if m.redisCache != nil {
		result := DomainCacheResult{Exists: exists}
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
