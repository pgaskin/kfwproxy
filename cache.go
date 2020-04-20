package main

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/dgraph-io/ristretto"
)

type Cache interface {
	Put(key string, data []byte, extra string, ttl time.Duration) (exp time.Time, ok bool)
	Get(key string) (data []byte, extra string, exp time.Time, ct time.Time, ok bool)
}

type RistrettoCache struct {
	r *ristretto.Cache
}

type ristrettoEnt struct {
	ct, exp time.Time
	data    []byte
	extra   string
}

func NewRistrettoCache(maxBytes int64) *RistrettoCache {
	r, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 10000,
		MaxCost:     maxBytes,
		BufferItems: 64,
		Metrics:     true,
	})
	if err != nil {
		panic(err)
	}
	return &RistrettoCache{r}
}

func (r *RistrettoCache) Put(key string, data []byte, extra string, ttl time.Duration) (time.Time, bool) {
	ct := time.Now()
	exp := ct.Add(ttl)
	return exp, r.r.SetWithTTL(key, ristrettoEnt{
		ct:    ct,
		exp:   exp,
		data:  data,
		extra: extra,
	}, int64(len(data)), ttl)
}

func (r *RistrettoCache) Get(key string) ([]byte, string, time.Time, time.Time, bool) {
	if enti, ok := r.r.Get(key); ok {
		ent := enti.(ristrettoEnt)
		return ent.data, ent.extra, ent.exp, ent.ct, true
	} else {
		return nil, "", time.Time{}, time.Time{}, false
	}
}

func (r *RistrettoCache) WritePrometheus(w io.Writer) {
	m := metrics.NewSet() // note: these metrics will be accurate to 2 seconds, since that's the current ristretto TTL cleanup interval
	m.NewGauge("kfwproxy_cache_len_count", func() float64 { return float64(int(r.r.Metrics.KeysAdded() - r.r.Metrics.KeysEvicted())) })
	m.NewGauge("kfwproxy_cache_size_count", func() float64 { return float64(int(r.r.Metrics.CostAdded() - r.r.Metrics.CostEvicted())) })
	m.NewCounter("kfwproxy_cache_hits_count").Set(r.r.Metrics.Hits())
	m.NewCounter("kfwproxy_cache_misses_count").Set(r.r.Metrics.Misses())
	m.NewCounter("kfwproxy_cache_puts_count").Set(r.r.Metrics.KeysAdded() + r.r.Metrics.KeysUpdated())
	m.WritePrometheus(w)
}

// StatsHandler is for backwards-compatibility.
func (r *RistrettoCache) StatsHandler(init time.Time) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "    ")
		enc.Encode(map[string]interface{}{
			"since":  init.String(),
			"for":    time.Now().Sub(init).String(),
			"len":    int(r.r.Metrics.KeysAdded() - r.r.Metrics.KeysEvicted()),
			"size":   int(r.r.Metrics.CostAdded() - r.r.Metrics.CostEvicted()),
			"hits":   r.r.Metrics.Hits(),
			"misses": r.r.Metrics.Misses(),
			"puts":   r.r.Metrics.KeysAdded() + r.r.Metrics.KeysUpdated(),
		})
	}
}
