package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/ogier/pflag"
)

func main() {
	addr := pflag.StringP("addr", "a", ":8080", "The address to listen on")
	timeout := pflag.DurationP("timeout", "t", time.Second*4, "Timeout for proxied requests")
	cacheLimit := pflag.Int64P("cache-limit", "l", 50, "Limit for cache size in MB")
	verbose := pflag.BoolP("verbose", "v", false, "Verbose logging")
	pflag.Parse()

	c := &http.Client{Timeout: *timeout}
	h := &memoryCache{Limit: *cacheLimit * 1000000, Verbose: *verbose}
	r := httprouter.New()

	r.GET("/", handler(http.RedirectHandler("https://github.com/geek1011/kfwproxy", http.StatusTemporaryRedirect)))
	r.GET("/stats", handler(http.HandlerFunc(h.handleStats)))

	r.GET("/api.kobobooks.com/1.0/UpgradeCheck/Device/:device/:affiliate/:version/:serial", handler(
		proxyHandler(c, true, true, h, time.Hour/2, []string{"X-Kobo-Accept-Preview"}, func(r *http.Request) string {
			return r.URL.String() + r.Header.Get("X-Kobo-Accept-Preview")
		}),
	))

	r.GET("/api.kobobooks.com/1.0/ReleaseNotes/:idx", handler(
		proxyHandler(c, true, true, h, time.Hour*3, nil, func(r *http.Request) string {
			return r.URL.String()
		}),
	))

	go func() {
		for range time.Tick(time.Minute) {
			h.Clean()
		}
	}()

	fmt.Printf("Listening on http://%s\n", *addr)
	if err := http.ListenAndServe(*addr, r); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handler(h http.Handler) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		h.ServeHTTP(w, r)
	}
}
