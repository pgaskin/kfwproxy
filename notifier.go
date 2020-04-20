package main

import (
	"fmt"
	"io"

	"github.com/VictoriaMetrics/metrics"
)

type Notifier interface {
	NotifyVersion(old, new Version)
}

type TelegramNotifier struct {
	t *Telegram
	c map[string]*cS
	m *metrics.Set
}

type cS struct {
	f    bool
	c, u string
	s, e *metrics.Counter
}

// NewTelegramNotifier creates a new TelegramNotifier. If any chats failed to
// register, each error is returned in the list. All chats in forcedChats must
// also be in chats or it will panic.
func NewTelegramNotifier(t *Telegram, chats []string, forcedChats []string) (*TelegramNotifier, []error) {
	var errs []error
	ac := make(map[string]*cS, len(chats))

	m := metrics.NewSet()
	m.NewGauge(`kfwproxy_telegram_chats_registered_count{bot="`+t.GetUsername()+`"}`, func() float64 { return float64(len(ac) - len(errs)) })
	m.NewGauge(`kfwproxy_telegram_chats_errored_count{bot="`+t.GetUsername()+`"}`, func() float64 { return float64(len(errs)) })

	for _, c := range chats {
		if _, ok := ac[c]; ok {
			panic(fmt.Sprintf("duplicate chat %#v", c))
		}
		u, err := t.GetChatUsername(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("initialize chat %#v: %w", c, err))
			continue
		}
		ac[c] = &cS{
			f: false,
			c: c,
			u: u,
			s: m.NewCounter(`kfwproxy_telegram_messages_sent_total{bot="` + t.GetUsername() + `",chat="` + u + `"}`),
			e: m.NewCounter(`kfwproxy_telegram_messages_errored_total{bot="` + t.GetUsername() + `",chat="` + u + `"}`),
		}
	}

	for _, fc := range forcedChats {
		var f bool
		for _, c := range chats {
			if fc == c {
				f = true
				break
			}
		}
		if !f {
			panic(fmt.Sprintf("chat %#v is not in %+s", fc, chats))
		}
		if _, ok := ac[fc]; ok {
			ac[fc].f = true
		}
	}

	return &TelegramNotifier{t, ac, m}, errs
}

func (t *TelegramNotifier) NotifyVersion(old, new Version) {
	for _, c := range t.c {
		if old.Zero() && !c.f {
			fmt.Printf("Telegram: not sending message to %s (%s) about (%s, %s) since original version is zero (i.e. kfwproxy just started).\n", c.u, c.c, old, new)
			continue
		}
		fmt.Printf("Telegram: sending message to %s (%s) about (%s, %s).\n", c.u, c.c, old, new)
		if err := t.t.SendMessage(c.c, fmt.Sprintf(`Kobo firmware <b>%s</b> has been released!`+"\n"+`<a href="https://pgaskin.net/KoboStuff/kobofirmware.html">More information.</a>`, new)); err != nil {
			c.e.Inc()
		} else {
			c.s.Inc()
		}
	}
}

func (t *TelegramNotifier) WritePrometheus(w io.Writer) {
	t.m.WritePrometheus(w)
}
