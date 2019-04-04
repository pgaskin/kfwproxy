package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// proxyHandler forwards the GET/HEAD request (everything after the root, use http.StripPrefix if not the base)
// to the URL and query params passed in the original URL. It also allows adding CORS headers and caching
// the response.
func proxyHandler(c *http.Client, https, cors bool, cache cache, cacheTime time.Duration, passHeaders []string, genID func(r *http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := genID(r)

		w.Header().Set("Server", "kfwproxy")

		if cors {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET,HEAD,OPTIONS")
		}

		if r.Method == "OPTIONS" {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		buf, extra, exp, ct, ok := cache.Get(id)
		if ok {
			w.Header().Set("Expires", exp.Format(http.TimeFormat))
			w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%.0f", time.Now().Sub(exp).Seconds()))
			w.Header().Set("Content-Type", extra)
			w.Header().Set("X-KFWProxy-Cached", ct.Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
			w.Write(buf)
			return
		}

		p := strings.TrimLeft(r.URL.Path, "/")
		if q := r.URL.RawQuery; q != "" {
			p += "?" + q
		}

		u, err := url.Parse(p)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err)
			fmt.Fprintf(os.Stderr, "Error proxying request '%s' to '%s': %v\n", r.URL, p, err)
			return
		}

		if https {
			u.Scheme = "https"
		} else {
			u.Scheme = "http"
		}

		nr, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err)
			fmt.Fprintf(os.Stderr, "Error proxying request '%s' to '%s': %v\n", r.URL, u, err)
			return
		}

		for _, h := range passHeaders {
			if v := r.Header.Get(h); v != "" {
				nr.Header.Set(h, v)
			}
		}

		nr.Header.Set("User-Agent", "kfwproxy (github.com/geek1011/kfwproxy)")

		resp, err := c.Do(nr)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err)
			fmt.Fprintf(os.Stderr, "Error proxying request '%s' to '%s': %v\n", r.URL, nr.URL, err)
			return
		}
		defer resp.Body.Close()

		buf, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err)
			fmt.Fprintf(os.Stderr, "Error proxying request '%s' to '%s': %v\n", r.URL, nr.URL, err)
			return
		}

		w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))

		if resp.StatusCode == http.StatusOK {
			exp, ok := cache.Put(id, buf, resp.Header.Get("Content-Type"), cacheTime)
			if ok {
				w.Header().Set("Expires", exp.Format(http.TimeFormat))
				w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%.0f", time.Now().Sub(exp).Seconds()))
				w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
				w.Header().Set("X-KFWProxy-Cached", "new")
			}
		} else {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-KFWProxy-Cached", "no")
		}

		w.WriteHeader(resp.StatusCode)
		if r.Method != "HEAD" {
			w.Write(buf)
		}
	})
}
