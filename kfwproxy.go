package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/spf13/pflag"
)

func main() {
	addr := pflag.StringP("addr", "a", ":8080", "The address to listen on")
	timeout := pflag.DurationP("timeout", "t", time.Second*4, "Timeout for proxied requests")
	cacheLimit := pflag.Int64P("cache-limit", "l", 50, "Limit for cache size in MB")
	verbose := pflag.BoolP("verbose", "v", false, "Verbose logging")

	envmap := map[string]string{
		"addr":        "KFWPROXY_ADDR",
		"timeout":     "KFWPROXY_TIMEOUT",
		"cache-limit": "KFWPROXY_CACHE_LIMIT",
		"verbose":     "KFWPROXY_VERBOSE",
	}

	if val, ok := os.LookupEnv("PORT"); ok {
		val = ":" + val
		fmt.Printf("Setting --addr from PORT to %#v\n", val)
		if err := pflag.Set("addr", val); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(2)
		}
	}

	pflag.VisitAll(func(flag *pflag.Flag) {
		if env, ok := envmap[flag.Name]; ok {
			flag.Usage += fmt.Sprintf(" (env %s)", env)
			if val, ok := os.LookupEnv(env); ok {
				fmt.Printf("Setting --%s from %s to %#v\n", flag.Name, env, val)
				if err := flag.Value.Set(val); err != nil {
					fmt.Printf("Error: %v\n", err)
					os.Exit(2)
				}
			}
		}
	})

	pflag.Parse()

	c := &http.Client{Timeout: *timeout}
	h := newMemoryCache(*cacheLimit*1000000, *verbose)
	l := &latestTracker{}
	r := httprouter.New()

	go h.CleanEvery(time.Minute)

	r.GET("/", handler(http.RedirectHandler("https://github.com/geek1011/kfwproxy", http.StatusTemporaryRedirect)))
	r.GET("/stats", handler(http.HandlerFunc(h.HandleStats)))
	r.GET("/latest/notes", handler(http.HandlerFunc(l.HandleNotes)))
	r.GET("/latest/version", handler(http.HandlerFunc(l.HandleVersion)))
	r.GET("/latest/version/svg", handler(http.HandlerFunc(l.HandleVersionSVG)))
	r.GET("/latest/version/png", handler(http.HandlerFunc(l.HandleVersionPNG)))
	r.GET("/latest/notes/redir", handler(http.HandlerFunc(l.HandleNotesRedir)))
	r.GET("/latest/version/redir", handler(http.HandlerFunc(l.HandleVersionRedir)))

	r.GET("/metrics", handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.WritePrometheus(w)
		l.WritePrometheus(w)
	})))

	r.GET("/api.kobobooks.com/1.0/UpgradeCheck/Device/:device/:affiliate/:version/:serial", handler(
		proxyHandler(c, true, true, h, time.Hour/2, []string{"X-Kobo-Accept-Preview"}, func(r *http.Request) string {
			return r.URL.String() + r.Header.Get("X-Kobo-Accept-Preview")
		}, l.InterceptUpgradeCheck),
	))

	r.GET("/api.kobobooks.com/1.0/ReleaseNotes/:idx", handler(
		proxyHandler(c, true, true, h, time.Hour*3, nil, func(r *http.Request) string {
			return r.URL.String()
		}, nil),
	))

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
