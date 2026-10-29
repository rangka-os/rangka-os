// Package cache is the kernel cache capability (system.rk cache:): an interface with an in-memory TTL
// driver and a Valkey stub.
package cache

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Cache stores values for a while.
type Cache interface {
	Get(key string) (any, bool)
	Set(key string, v any, ttl time.Duration)
	Delete(key string)
	DeletePrefix(prefix string)
	Stats() Stats
}

// Stats for /system/status.
type Stats struct {
	Driver string `json:"driver"`
	OK     bool   `json:"ok"`
	Hits   int64  `json:"hits"`
	Misses int64  `json:"misses"`
	Keys   int    `json:"keys"`
}

type entry struct {
	v   any
	exp time.Time
}

// Memory is an in-process TTL cache bounded by entry count.
type Memory struct {
	mu           sync.Mutex
	m            map[string]entry
	max          int
	hits, misses atomic.Int64
}

// NewMemory makes a memory cache; maxBytes is approximated as 1 KiB per entry.
func NewMemory(maxBytes int) *Memory {
	n := maxBytes / 1024
	if n < 128 {
		n = 128
	}
	return &Memory{m: map[string]entry{}, max: n}
}

func (c *Memory) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || (!e.exp.IsZero() && time.Now().After(e.exp)) {
		if ok {
			delete(c.m, key)
		}
		c.misses.Add(1)
		return nil, false
	}
	c.hits.Add(1)
	return e.v, true
}

func (c *Memory) Set(key string, v any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		now := time.Now()
		for k, e := range c.m {
			if !e.exp.IsZero() && now.After(e.exp) {
				delete(c.m, k)
			}
		}
		for k := range c.m {
			if len(c.m) < c.max {
				break
			}
			delete(c.m, k)
		}
	}
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.m[key] = entry{v, exp}
}

func (c *Memory) Delete(key string) {
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
}

func (c *Memory) DeletePrefix(prefix string) {
	c.mu.Lock()
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			delete(c.m, k)
		}
	}
	c.mu.Unlock()
}

func (c *Memory) Stats() Stats {
	c.mu.Lock()
	n := len(c.m)
	c.mu.Unlock()
	return Stats{Driver: "memory", OK: true, Hits: c.hits.Load(), Misses: c.misses.Load(), Keys: n}
}

// Valkey is a placeholder for the Valkey/Redis driver: it caches nothing and reports itself as not ready.
// Swap in a real client (RESP over TCP) when a Valkey server is configured.
type Valkey struct {
	URL    string
	misses atomic.Int64
}

func (v *Valkey) Get(string) (any, bool)         { v.misses.Add(1); return nil, false }
func (v *Valkey) Set(string, any, time.Duration) {}
func (v *Valkey) Delete(string)                  {}
func (v *Valkey) DeletePrefix(string)            {}
func (v *Valkey) Stats() Stats                   { return Stats{Driver: "valkey", OK: false, Misses: v.misses.Load()} }

// New picks a driver from system.rk cache settings.
func New(driver string, maxMB int, url string) Cache {
	if driver == "valkey" {
		return &Valkey{URL: url}
	}
	if maxMB <= 0 {
		maxMB = 64
	}
	return NewMemory(maxMB << 20)
}

// ParseTTL parses "5m", "30s", "1h".
func ParseTTL(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
