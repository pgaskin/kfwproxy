package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

type cache interface {
	Put(id string, buf []byte, extra string, exp time.Duration) (cexp time.Time, ok bool)
	Get(id string) (buf []byte, extra string, exp time.Time, ct time.Time, ok bool)
}

type memoryCache struct {
	Limit   int64
	Verbose bool
	size    int64
	entries map[string]*memoryCacheEntry
	mu      sync.RWMutex

	init               time.Time
	hits, puts, misses int64
}

// CleanEvery runs Clean at a specified interval. This is intended to be run as a goroutine.
func (c *memoryCache) CleanEvery(i time.Duration) {
	for range time.Tick(i) {
		c.Clean()
	}
}

// Clean cleans up expired cache entries and removes entries if over the size limit.
func (c *memoryCache) Clean() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Verbose {
		fmt.Printf("memoryCache.Clean: running cleanup size=%d/%d entries=%d\n", c.size, c.Limit, len(c.entries))
	}

	for id, entry := range c.entries {
		if time.Now().After(entry.exp) {
			if c.Verbose {
				fmt.Printf("memoryCache.Clean: cache expired: removed %#v\n", id)
			}
			c.size -= int64(len(c.entries[id].buf))
			delete(c.entries, id)
		}
	}

	// TODO: this is inefficient
	if c.Limit > 0 && c.size > c.Limit {
		ids, times := make([]string, len(c.entries)), make([]time.Time, len(c.entries))
		var i int
		for id, entry := range c.entries {
			ids[i] = id
			times[i] = entry.ct
			i++
		}
		sort.Sort(customSort{
			Lenf: func() int {
				return len(ids)
			},
			Lessf: func(i, j int) bool {
				return times[i].Before(times[j])
			},
			Swapf: func(i, j int) {
				ids[i], ids[j] = ids[j], ids[i]
				times[i], times[j] = times[j], times[i]
			},
		})
		for _, id := range ids {
			c.size -= int64(len(c.entries[id].buf))
			delete(c.entries, id)
			if c.Verbose {
				fmt.Printf("memoryCache.Clean: cache size %d/%d: removed %#v\n", c.size, c.Limit, id)
			}
			if c.size < c.Limit {
				break
			}
		}
	}

	debug.FreeOSMemory()
}

func (c *memoryCache) Put(id string, buf []byte, extra string, exp time.Duration) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Limit > 0 && int64(len(buf)) > c.Limit {
		if c.Verbose {
			fmt.Printf("memoryCache.Put: cache too full: %s: %s\n", exp, id)
		}
		return time.Time{}, false
	}

	if c.Verbose {
		v := len(buf)
		if v > 20 {
			v = 20
		}
		fmt.Printf("memoryCache.Put: cache put: %d/%d: %s: %s: %#v...\n", c.size, c.Limit, exp, id, string(buf)[:v])
	}

	c.puts++
	t := time.Now()
	c.entries[id] = &memoryCacheEntry{ct: t, exp: t.Add(exp), buf: buf, extra: extra}
	c.size += int64(len(buf))
	return t.Add(exp), true
}

func (c *memoryCache) Get(id string) (buf []byte, extra string, exp time.Time, ct time.Time, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ent, f := c.entries[id]
	if !f || ent == nil || time.Now().After(ent.exp) {
		if c.Verbose {
			fmt.Printf("memoryCache.Get: cache miss: %s\n", id)
		}
		c.misses++
		return nil, "", time.Time{}, time.Time{}, false
	}

	if c.Verbose {
		fmt.Printf("memoryCache.Get: cache get: %s\n", id)
	}

	c.hits++
	return ent.buf, ent.extra, ent.exp, ent.ct, true
}

func newMemoryCache(limitBytes int64, verbose bool) *memoryCache {
	return &memoryCache{
		entries: map[string]*memoryCacheEntry{},
		init:    time.Now(),
	}
}

func (c *memoryCache) HandleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "    ")
	enc.Encode(map[string]interface{}{
		"since":  c.init.String(),
		"for":    time.Now().Sub(c.init).String(),
		"len":    len(c.entries),
		"size":   c.size,
		"hits":   c.hits,
		"misses": c.misses,
		"puts":   c.puts,
	})
}

func (c *memoryCache) WritePrometheus(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	m := metrics.NewSet()
	m.NewCounter("kfwproxy_uptime_seconds_total").Set(uint64(int(time.Now().Sub(c.init).Seconds())))
	m.NewGauge("kfwproxy_cache_len_count", func() float64 { return float64(len(c.entries)) })
	m.NewGauge("kfwproxy_cache_size_bytes", func() float64 { return float64(c.size) })
	m.NewCounter("kfwproxy_cache_hits_count").Set(uint64(c.hits))
	m.NewCounter("kfwproxy_cache_misses_count").Set(uint64(c.misses))
	m.NewCounter("kfwproxy_cache_puts_count").Set(uint64(c.puts))
	m.WritePrometheus(w)
}

type memoryCacheEntry struct {
	ct, exp time.Time
	buf     []byte
	extra   string
}

type customSort struct {
	Lenf  func() int
	Lessf func(i, j int) bool
	Swapf func(i, j int)
}

func (s customSort) Len() int {
	return s.Lenf()
}

func (s customSort) Less(i, j int) bool {
	return s.Lessf(i, j)
}

func (s customSort) Swap(i, j int) {
	s.Swapf(i, j)
}
