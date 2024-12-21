package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
)

// ProxyHandler forwards the GET/OPTIONS/HEAD request (everything after the
// root, use http.StripPrefix if not the base) to the URL and query params
// passed in the original URL.
type ProxyHandler struct {
	// request
	Client        *http.Client // optional
	DefaultScheme string       // optional (default: http)
	PassHeaders   []string     // optional
	UserAgent     string       // optional

	// response
	KeepHeaders []string // optional (default: Content-Type)

	// response transformation, processed immediately before writing the response (i.e. not stored in the cache)
	Server string                      // optional
	CORS   bool                        // optional
	Hook   func(*http.Request, []byte) // optional

	// cache
	Cache    Cache                      // optional
	CacheTTL time.Duration              // optional (default: 1h)
	CacheID  func(*http.Request) string // required if Cache set, passed the user's request, not the upstream one
}

func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var log zerolog.Logger
	if hl := hlog.FromRequest(r); hl != nil {
		log = hl.With().Str("component", "proxy").Logger()
	} else {
		log = zerolog.Nop()
	}

	if r.Method == "OPTIONS" {
		p.transformHeaders(r, w)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		log.Warn().Msg("method not allowed")
		p.transformHeaders(r, w)
		w.Header().Del("Content-Length")
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	var status int
	var buf []byte
	var hdr http.Header
	var cached string
	var exp time.Time

	if p.Cache != nil {
		if cbuf, chdr, cexp, ct, ok := p.Cache.Get(p.CacheID(r)); ok {
			log.Debug().
				Time("cache_time", ct).
				Time("cache_expiry", cexp).
				Msg("serving from cache")
			status, buf, hdr = http.StatusOK, cbuf, chdr
			cached, exp = ct.Format(http.TimeFormat), cexp
		}
	}

	if cached == "" {
		log.Debug().Msg("making upstream request")
		ustatus, ubuf, uhdr, err := p.upstream(r, log)
		if err != nil {
			p.transformHeaders(r, w)
			w.Header().Del("Content-Length")
			log.Err(err).Msg("upstream")
			http.Error(w, fmt.Sprintf("%s: proxy %#v: %v", r.URL.String(), http.StatusText(http.StatusBadGateway), err), http.StatusBadGateway)
			return
		}
		status, buf, hdr = ustatus, ubuf, uhdr
		if ustatus == http.StatusOK && p.Cache != nil {
			if uexp, ok := p.Cache.Put(p.CacheID(r), ubuf, uhdr, p.CacheTTL); ok {
				cached, exp = "new", uexp
			} else {
				cached, exp = "nospace", time.Now().Add(p.CacheTTL)
			}
		} else {
			cached, exp = "no", time.Time{}
		}
	}

	log.Info().
		Int("status", status).
		Str("cached", cached).
		Time("expiry", exp).
		Msg("response")

	for k, v := range hdr {
		w.Header()[k] = v
	}
	p.transformHeaders(r, w)
	p.transformResponse(r, buf)

	w.Header().Set("X-KFWProxy-Cached", cached)
	if cached == "no" { // no cache available
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		if exp.IsZero() {
			panic("cached, but no expiry!?!")
		}
		w.Header().Set("Expires", exp.Format(http.TimeFormat))
		w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%.0f", exp.Sub(time.Now()).Seconds()))
	}

	if r.Method == "HEAD" {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(status)
	} else {
		w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
		w.WriteHeader(status)
		w.Write(buf)
	}
}

func (p *ProxyHandler) upstream(r *http.Request, log zerolog.Logger) (int, []byte, http.Header, error) {
	u, err := url.Parse(strings.TrimLeft(r.URL.Path, "/"))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("extract upstream URL from %#v: %w", r.URL, err)
	}
	u.RawQuery = r.URL.RawQuery

	if u.Scheme == "" {
		if p.DefaultScheme == "" {
			u.Scheme = "http"
		} else {
			u.Scheme = p.DefaultScheme
		}
	}

	nr, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("create upstream request %#v: %w", u.String(), err)
	}

	for _, k := range p.PassHeaders {
		if v := r.Header.Values(k); v != nil {
			nr.Header[k] = v
		}
	}
	if p.UserAgent != "" {
		nr.Header.Set("User-Agent", p.UserAgent)
	}

	log.Debug().
		Str("method", nr.Method).
		Str("url", nr.URL.String()).
		Msg("sending upstream request")

	var resp *http.Response
	if p.Client == nil {
		resp, err = http.DefaultClient.Do(nr)
	} else {
		resp, err = p.Client.Do(nr)
	}
	if err != nil {
		return 0, nil, nil, fmt.Errorf("do upstream request to %#v: %w", u.String(), err)
	}
	defer resp.Body.Close()

	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read upstream response for %#v: %w", u.String(), err)
	}

	hdr := make(http.Header)
	if p.KeepHeaders == nil { // len(0) is different
		hdr["Content-Type"] = resp.Header.Values("Content-Type")
	} else {
		for _, k := range p.KeepHeaders {
			hdr[k] = resp.Header.Values(k)
		}
	}

	return resp.StatusCode, buf, hdr, nil
}

func (p *ProxyHandler) transformHeaders(r *http.Request, w http.ResponseWriter) {
	if p.Server != "" {
		w.Header().Add("Server", p.Server)
	}
	if p.CORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "X-KFWProxy-Request-ID, X-KFWProxy-Cached")
	}
}

func (p *ProxyHandler) transformResponse(r *http.Request, buf []byte) {
	if p.Hook != nil {
		p.Hook(r, buf)
	}
}
