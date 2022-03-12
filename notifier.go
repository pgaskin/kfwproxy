package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/VictoriaMetrics/metrics"
	"github.com/rs/zerolog"
)

type Notifier interface {
	NotifyVersion(old, new Version)
}

type TelegramNotifier struct {
	t   *Telegram
	c   map[string]*cS
	m   *metrics.Set
	log zerolog.Logger
}

type cS struct {
	f    bool
	c, u string
	s, e *metrics.Counter
}

// NewTelegramNotifier creates a new TelegramNotifier. If any chats failed to
// register, each error is returned in the list. All chats in forcedChats must
// also be in chats or it will panic.
func NewTelegramNotifier(t *Telegram, chats []string, forcedChats []string, log zerolog.Logger) (*TelegramNotifier, []error) {
	var errs []error
	ac := make(map[string]*cS, len(chats))

	m := metrics.NewSet()
	m.NewGauge(`kfwproxy_telegram_chats_registered_count{bot="`+t.GetUsername()+`"}`, func() float64 { return float64(len(ac)) })
	m.NewGauge(`kfwproxy_telegram_chats_errored_count{bot="`+t.GetUsername()+`"}`, func() float64 { return float64(len(errs)) })

	log.Info().Msg("Initializing chats")
	for _, c := range chats {
		if _, ok := ac[c]; ok {
			log.Fatal().Msgf("Duplicate chat %#v", c)
			panic("")
		}
		u, err := t.GetChatUsername(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("initialize chat %#v: %w", c, err))
			log.Err(err).Msgf("Could not initialize chat %#v", c)
			continue
		}
		log.Info().
			Str("id", c).
			Str("username", u).
			Msgf("Sending notifications to %#v (%s) via %#v", u, c, t.GetUsername())
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

	return &TelegramNotifier{t, ac, m, log}, errs
}

func (t *TelegramNotifier) NotifyVersion(old, new Version) {
	t.log.Info().
		Str("old", old.String()).
		Str("new", new.String()).
		Msgf("sending notifications about %s", new)
	for _, c := range t.c {
		if old.Zero() && !c.f {
			t.log.Info().
				Str("id", c.c).
				Str("username", c.u).
				Msgf("not sending message to %s (%s) about (%s, %s) since original version is zero (i.e. kfwproxy just started)", c.u, c.c, old, new)
			continue
		}
		t.log.Info().
			Str("id", c.c).
			Str("username", c.u).
			Msgf("sending message to %s (%s) about (%s, %s)", c.u, c.c, old, new)
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

type MobileReadNotifier struct {
	mr  *MobileRead
	f   map[int]*fS
	m   *metrics.Set
	log zerolog.Logger
}

type fS struct {
	f    bool
	fi   int
	s, e *metrics.Counter
}

func NewMobileReadNotifier(mr *MobileRead, forums []int, forcedForums []int, log zerolog.Logger) (*MobileReadNotifier, []error) {
	var errs []error
	af := make(map[int]*fS, len(forums))

	m := metrics.NewSet()
	m.NewGauge(`kfwproxy_mobileread_forums_count{username="`+mr.GetUsername()+`"}`, func() float64 { return float64(len(af)) })

	if err := mr.Login(); err != nil {
		log.Err(err).Msg("could not log into MobileRead")
	}

	log.Info().Msg("initializing chats")
	for _, fi := range forums {
		if _, ok := af[fi]; ok {
			log.Fatal().Msgf("duplicate forum %d", fi)
			panic("")
		}
		log.Info().
			Int("forum", fi).
			Msgf("posting threads to %d via %q", fi, mr.GetUsername())
		af[fi] = &fS{
			f:  false,
			fi: fi,
			s:  m.NewCounter(`kfwproxy_mobileread_threads_posted_total{username="` + mr.GetUsername() + `",forum="` + strconv.Itoa(fi) + `"}`),
			e:  m.NewCounter(`kfwproxy_mobileread_threads_errored_total{username="` + mr.GetUsername() + `",forum="` + strconv.Itoa(fi) + `"}`),
		}
	}

	for _, ffi := range forcedForums {
		var f bool
		for _, c := range forums {
			if ffi == c {
				f = true
				break
			}
		}
		if !f {
			panic(fmt.Sprintf("forum %d is not in %+d", ffi, forums))
		}
		if _, ok := af[ffi]; ok {
			af[ffi].f = true
		}
	}

	return &MobileReadNotifier{mr, af, m, log}, errs
}

func (m *MobileReadNotifier) NotifyVersion(old, new Version) {
	m.log.Info().
		Str("old", old.String()).
		Str("new", new.String()).
		Msgf("posting threads about %s", new)
	for _, f := range m.f {
		if old.Zero() && !f.f {
			m.log.Info().
				Int("forum", f.fi).
				Msgf("not posting thread to %d about (%s, %s) since original version is zero (i.e. kfwproxy just started)", f.fi, old, new)
			continue
		}
		m.log.Info().
			Int("forum", f.fi).
			Msgf("posting thread to %d about (%s, %s)", f.fi, old, new)
		if tid, err := m.mr.NewThread(f.fi, fmt.Sprintf(`Firmware %s`, new), fmt.Sprintf(`Firmware %s has been released.`+"\n\n"+`[SIZE=1][COLOR=#999][I]Automatically posted by [URL="https://kfw.api.pgaskin.net"]kfwproxy[/URL].[/I][/COLOR][/SIZE]`, new), "firmware, firmware release", true, false, true); err != nil {
			f.e.Inc()
		} else {
			f.s.Inc()
			m.log.Info().
				Int("forum", f.fi).
				Int("thread", tid).
				Msgf("posted thread %d in forum %d", tid, f.fi)
		}
	}
}

func (m *MobileReadNotifier) WritePrometheus(w io.Writer) {
	m.m.WritePrometheus(w)
}
