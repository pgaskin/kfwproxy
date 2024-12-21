package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/VictoriaMetrics/metrics"
	"github.com/julienschmidt/httprouter"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
	"github.com/spf13/pflag"
)

func main() {
	addr := pflag.StringP("addr", "a", ":8080", "the address to listen on")
	timeout := pflag.DurationP("timeout", "t", time.Second*4, "timeout for proxied requests")
	cacheLimit := pflag.Int64P("cache-limit", "l", 50, "limit for cache size in MB")
	cacheTime := pflag.DurationP("cache-time", "T", time.Hour/4, "how long to cache upgrade info for")
	telegramBot := pflag.StringP("telegram-bot", "B", "", "the Telegram bot token (to enable notifications) (requires telegram-chat)")
	telegramChat := pflag.StringSliceP("telegram-chat", "b", nil, "the Telegram chat IDs to send messages to (find it using @IDBot) (can also specify a channel in the format @ChannelUsername) (requires telegram-bot)")
	telegramForce := pflag.StringSlice("telegram-force", nil, "send Telegram messages to these chats even if the original version is zero (for debugging only)")
	mobilereadUser := pflag.StringP("mobileread-user", "M", "", "the MobileRead credentials (to enable notifications) (requires mobileread-forum) (format: username:password)")
	mobilereadForum := pflag.IntSliceP("mobileread-forum", "m", nil, "the MobileRead forum IDs to post threads to (requires mobileread-username and mobileread-password)")
	mobilereadForce := pflag.IntSlice("mobileread-force", nil, "post MobileRead threads to these chats even if the original version is zero (for debugging only)")
	logJSON := pflag.BoolP("log-json", "j", false, "use JSON for logs")
	logLevel := pflag.IntP("log-level", "v", 1, "log level (0=debug, 1=info, 2=warn, 3=error)")
	help := pflag.BoolP("help", "h", false, "show this help text")

	envmap := map[string]string{
		"addr":             "KFWPROXY_ADDR",
		"timeout":          "KFWPROXY_TIMEOUT",
		"cache-limit":      "KFWPROXY_CACHE_LIMIT",
		"cache-time":       "KFWPROXY_CACHE_TIME",
		"telegram-bot":     "KFWPROXY_TELEGRAM_BOT",
		"telegram-chat":    "KFWPROXY_TELEGRAM_CHAT",
		"telegram-force":   "KFWPROXY_TELEGRAM_FORCE",
		"mobileread-user":  "KFWPROXY_MOBILEREAD_USER",
		"mobileread-forum": "KFWPROXY_MOBILEREAD_FORUM",
		"mobileread-force": "KFWPROXY_MOBILEREAD_FORCE",
		"log-json":         "KFWPROXY_LOG_JSON",
		"log-level":        "KFWPROXY_LOG_LEVEL",
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

	var log zerolog.Logger
	if *logJSON {
		log = zerolog.New(os.Stdout)
	} else {
		log = zerolog.New(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			NoColor:    false,
			TimeFormat: time.ANSIC,
			PartsOrder: []string{zerolog.TimestampFieldName, zerolog.LevelFieldName, "component", zerolog.MessageFieldName},
		})
	}
	log = log.Level(zerolog.Level(*logLevel))
	log = log.With().Timestamp().Logger()

	if (*telegramBot == "") != (len(*telegramChat) == 0) {
		fmt.Fprintf(os.Stderr, "Error: Neither or both of telegram-bot and telegram-chat must be specified.\n")
		os.Exit(2)
		return
	}

	if (*mobilereadUser == "") != (len(*mobilereadForum) == 0) {
		fmt.Fprintf(os.Stderr, "Error: Neither or both of mobileread-user and mobileread-forum must be specified.\n")
		os.Exit(2)
		return
	}

	for _, fid := range *telegramForce {
		var f bool
		for _, id := range *telegramChat {
			if id == fid {
				f = true
			}
		}
		if !f {
			fmt.Fprintf(os.Stderr, "Error: All chat IDs in telegram-force must be specified in telegram-chat as well.\n")
			os.Exit(2)
			return
		}
	}

	if *mobilereadUser != "" && !strings.Contains(*mobilereadUser, ":") {
		fmt.Fprintf(os.Stderr, "Error: mobileread-user must contain a ':' if set.\n")
		os.Exit(2)
		return
	}

	for _, fid := range *mobilereadForce {
		var f bool
		for _, id := range *mobilereadForum {
			if id == fid {
				f = true
			}
		}
		if !f {
			fmt.Fprintf(os.Stderr, "Error: All forum IDs in mobileread-force must be specified in mobileread-forum as well.\n")
			os.Exit(2)
			return
		}
	}

	if pflag.NArg() != 0 || *help {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\nOptions:\n%s", os.Args[0], pflag.CommandLine.FlagUsages())
		if len(os.Args) != 1 {
			os.Exit(2)
		} else {
			os.Exit(1)
		}
		return
	}

	var p []interface{ WritePrometheus(io.Writer) }
	j, _ := cookiejar.New(nil)
	cl := &http.Client{Timeout: *timeout, Jar: j}
	uc := uptimeCounter(time.Now())
	c := NewRistrettoCache(*cacheLimit * 1000000)
	l := NewLatestTracker(log.With().Str("component", "latest").Logger())
	p = append(p, uc, c, l)

	if *telegramBot != "" {
		go func() {
			log.Info().Str("component", "kfwproxy").Msg("initializing Telegram")
			tc, err := NewTelegram(cl, *telegramBot)
			if err != nil {
				log.Err(err).Str("component", "kfwproxy").Msg("could not initialize Telegram bot")
				return
			}
			tn, _ := NewTelegramNotifier(tc, *telegramChat, *telegramForce, log.With().Str("component", "telegram").Logger())
			l.Notify(tn)
			p = append(p, tn)
			log.Info().Str("component", "kfwproxy").Msg("initialized Telegram")
		}()
	}

	if *mobilereadUser != "" {
		go func() {
			log.Info().Str("component", "kfwproxy").Msg("initializing MobileRead")
			spl := strings.SplitN(*mobilereadUser, ":", 2)
			mr, err := NewMobileRead(cl, spl[0], spl[1])
			if err != nil {
				log.Err(err).Str("component", "kfwproxy").Msg("could not initialize MobileRead user")
				return
			}
			mn, _ := NewMobileReadNotifier(mr, *mobilereadForum, *mobilereadForce, log.With().Str("component", "mobileread").Logger())
			l.Notify(mn)
			p = append(p, mn)
			log.Info().Str("component", "kfwproxy").Msg("initialized MobileRead")
		}()
	}

	r := httprouter.New()

	r.Handler("GET", "/", http.RedirectHandler("https://github.com/pgaskin/kfwproxy", http.StatusTemporaryRedirect))

	for _, v := range []struct {
		u string
		h *ProxyHandler
	}{
		{"/api.kobobooks.com/1.0/UpgradeCheck/Device/:device/:affiliate/:version/:serial", &ProxyHandler{
			PassHeaders: []string{"X-Kobo-Accept-Preview"},
			Hook:        func(r *http.Request, buf []byte) { go l.InterceptUpgradeCheck(buf) },
			CacheTTL:    *cacheTime,
			CacheID:     func(r *http.Request) string { return r.URL.String() + r.Header.Get("X-Kobo-Accept-Preview") },
		}},
		{"/api.kobobooks.com/1.0/ReleaseNotes/:idx", &ProxyHandler{
			CacheTTL: time.Hour * 3,
			CacheID:  func(r *http.Request) string { return r.URL.String() },
		}},
	} {
		v.h.Client = cl
		v.h.UserAgent = "kfwproxy (github.com/pgaskin/kfwproxy)"
		v.h.Server = "kfwproxy"
		v.h.CORS = true
		v.h.Cache = c
		for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
			r.Handler(m, v.u, v.h)
		}
	}

	r.HandlerFunc("GET", "/stats", c.StatsHandler(time.Time(uc)))
	r.HandlerFunc("GET", "/metrics", func(w http.ResponseWriter, r *http.Request) {
		for _, m := range p {
			m.WritePrometheus(w)
		}
	})

	l.Mount(r)

	hdl := hlog.NewHandler(log)(hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		hlog.FromRequest(r).Debug().
			Str("component", "http").
			Str("method", r.Method).
			Str("url", r.URL.String()).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("handled")
	})(hlog.RequestIDHandler("request_id", "X-KFWProxy-Request-ID")(r)))

	r.HandlerFunc("OPTIONS", "/api.kobobooks.com", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.Header().Set("Server", "kfwproxy")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "X-KFWProxy-Request-ID")
		w.WriteHeader(http.StatusOK)
		return
	})

	r.Handler("GET", "/api.kobobooks.com", func(hdl http.Handler) http.Handler {
		type batchKey string
		const batched = batchKey("batched")
		return gziphandler.GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var log zerolog.Logger
			if hl := hlog.FromRequest(r); hl != nil {
				log = hl.With().Str("component", "batch").Logger()
			} else {
				log = zerolog.Nop()
			}

			w.Header().Set("Server", "kfwproxy")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "X-KFWProxy-Request-ID")

			if r.Context().Value(batched) != nil {
				log.Warn().Msg("recursive batch")
				http.Error(w, "Batch recursion not allowed", http.StatusForbidden)
				return
			}

			xs := r.URL.Query()["x"]
			if len(xs) == 0 {
				http.Error(w, "Parameter x[] missing for batch GET", http.StatusBadRequest)
				return
			}
			if len(xs) > 20 {
				log.Warn().Msg("too many requests in batch GET")
				http.Error(w, "Too many requests in batch GET", http.StatusForbidden)
				return
			}

			hd := r.URL.Query().Get("h")
			if hd != "" && hd != "1" {
				http.Error(w, "Parameter h must be 1 or unset for batch GET", http.StatusBadRequest)
				return
			}

			log.Info().Int("n", len(xs)).Msg("processing batch request")

			res := make([]struct {
				Status int                 `json:"status"`
				Header map[string][]string `json:"header,omitempty"`
				Body   string              `json:"body"`
			}, len(xs))

			cache, noCache := int((*cacheTime).Seconds()), false

			for i, x := range xs {
				x = "/api.kobobooks.com/" + strings.TrimPrefix(x, "/")

				rc := httptest.NewRecorder()
				rq, err := http.NewRequestWithContext(context.WithValue(r.Context(), batched, true), "GET", x, nil)
				if err != nil {
					res[i].Status = http.StatusBadRequest
					res[i].Body = err.Error()
					continue
				}

				hdl.ServeHTTP(rc, rq)

				// cache for the minimum max-age if all requests are successful
				if !noCache {
					if rc.Code != http.StatusOK {
						noCache = true
					} else if cc := rc.HeaderMap.Get("Cache-Control"); cc != "" { // kfwproxy endpoints return Cache-Control or nothing, so we don't need to handle Expires or the other ones
						for _, ccs := range strings.Split(cc, ",") {
							if strings.HasPrefix(strings.TrimSpace(ccs), "max-age=") {
								if c, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(ccs, "=", 2)[1])); err != nil {
									continue
								} else {
									if c <= 0 {
										noCache = true
									} else if c < cache {
										cache = c
									}
								}
							}
						}
					}
				}

				res[i].Status = rc.Code
				if hd == "1" {
					res[i].Header = rc.HeaderMap
				}
				res[i].Body = rc.Body.String() // note: if binary responses are added anywhere in the future, it will need to be checked and return an error instead
			}

			if noCache {
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
			} else {
				w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(cache))
				w.Header().Set("Expires", time.Now().Add(time.Duration(cache)*time.Second).Format(http.TimeFormat))
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			enc := json.NewEncoder(w)
			enc.SetEscapeHTML(false)
			enc.Encode(res)
		}))
	}(hdl))

	log.Info().
		Str("component", "kfwproxy").
		Str("addr", *addr).
		Msgf("Listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, hdl); err != nil {
		log.Fatal().
			Str("component", "kfwproxy").
			AnErr("err", err).
			Msg("could not start server")
		os.Exit(1)
	}
}

type uptimeCounter time.Time

func (c uptimeCounter) WritePrometheus(w io.Writer) {
	m := metrics.NewSet()
	m.NewCounter("kfwproxy_uptime_seconds_total").Set(uint64(int(time.Now().Sub(time.Time(c)).Seconds())))
	m.WritePrometheus(w)
}
