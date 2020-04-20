package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/spf13/pflag"
)

func main() {
	addr := pflag.StringP("addr", "a", ":8080", "the address to listen on")
	timeout := pflag.DurationP("timeout", "t", time.Second*4, "timeout for proxied requests")
	cacheLimit := pflag.Int64P("cache-limit", "l", 50, "limit for cache size in MB")
	telegramBot := pflag.StringP("telegram-bot", "B", "", "the Telegram bot token (to enable notifications) (requires telegram-chat)")
	telegramChat := pflag.StringSliceP("telegram-chat", "b", nil, "the Telegram chat IDs to send messages to (find it using @IDBot) (can also specify a channel in the format @ChannelUsername) (requires telegram-bot)")
	telegramForce := pflag.StringSlice("telegram-force", nil, "send Telegram messages to these chats even if the original version is zero (for debugging only)")
	verbose := pflag.BoolP("verbose", "v", false, "Verbose logging")
	help := pflag.BoolP("help", "h", false, "show this help text")

	envmap := map[string]string{
		"addr":           "KFWPROXY_ADDR",
		"timeout":        "KFWPROXY_TIMEOUT",
		"cache-limit":    "KFWPROXY_CACHE_LIMIT",
		"telegram-bot":   "KFWPROXY_TELEGRAM_BOT",
		"telegram-chat":  "KFWPROXY_TELEGRAM_CHAT",
		"telegram-force": "KFWPROXY_TELEGRAM_FORCE",
		"verbose":        "KFWPROXY_VERBOSE",
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

	if (*telegramBot == "") != (len(*telegramChat) == 0) {
		fmt.Fprintf(os.Stderr, "Error: Neither or both of telegram-bot and telegram-chat must be specified.\n")
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

	if pflag.NArg() != 0 || *help {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\nOptions:\n%s", os.Args[0], pflag.CommandLine.FlagUsages())
		if len(os.Args) != 1 {
			os.Exit(2)
		} else {
			os.Exit(1)
		}
		return
	}

	var p []interface {
		WritePrometheus(io.Writer)
	}

	c := &http.Client{Timeout: *timeout}

	h := newMemoryCache(*cacheLimit*1000000, *verbose)
	go h.CleanEvery(time.Minute)
	p = append(p, h)

	l := NewLatestTracker()
	p = append(p, l)

	if *telegramBot != "" {
		go func() {
			fmt.Printf("Telegram: initializing.\n")
			tc, err := NewTelegram(c, *telegramBot)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Telegram: error: initialize bot: %v.\n", err)
				return
			}
			tn, errs := NewTelegramNotifier(tc, *telegramChat, *telegramForce)
			for _, err := range errs {
				fmt.Fprintf(os.Stderr, "Telegram: error: %v.\n", err)
			}
			fmt.Printf("Telegram: sending notifications to %+s via %s.\n", *telegramChat, tc.GetUsername())
			l.Notify(tn)
			p = append(p, tn)
		}()
	}

	r := httprouter.New()
	r.GET("/", handler(http.RedirectHandler("https://github.com/geek1011/kfwproxy", http.StatusTemporaryRedirect)))
	r.GET("/stats", handler(http.HandlerFunc(h.HandleStats)))
	r.GET("/latest/notes", handler(http.HandlerFunc(l.HandleNotes)))
	r.GET("/latest/version", handler(http.HandlerFunc(l.HandleVersion)))
	r.GET("/latest/version/svg", handler(http.HandlerFunc(l.HandleVersionSVG)))
	r.GET("/latest/version/png", handler(http.HandlerFunc(l.HandleVersionPNG)))
	r.GET("/latest/notes/redir", handler(http.HandlerFunc(l.HandleNotesRedir)))
	r.GET("/latest/version/redir", handler(http.HandlerFunc(l.HandleVersionRedir)))

	r.GET("/metrics", handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, m := range p {
			m.WritePrometheus(w)
		}
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
