package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/julienschmidt/httprouter"
	"github.com/pbnjay/pixfont"
	"github.com/rs/zerolog"
)

type LatestTracker struct {
	n []Notifier
	// note: this is more efficient than a mutex, and ordering isn't critical
	// because we only update it for a new version and it's nearly impossible
	// that multiple versions will be released at the exact same instant and
	// will disappear at the next one.
	v   atomic.Value
	t   atomic.Value
	log zerolog.Logger
}

type vS struct {
	v Version
	u string
}

type tS struct {
	t uint64
	u string
}

func NewLatestTracker(log zerolog.Logger) *LatestTracker {
	l := &LatestTracker{log: log}

	// note: this must be initialized in this way, as an atomic.Value can't be copied after being stored
	l.v.Store(vS{})
	l.t.Store(tS{})

	go l.notify()
	return l
}

func (l *LatestTracker) Notify(n ...Notifier) {
	l.n = append(l.n, n...)
}

// notify watches for version changes every 5 seconds. This is done to prevent
// false positives for new versions if the affiliates are not all on the same
// version during the first set of requests when kfwproxy starts.
func (l *LatestTracker) notify() {
	var o Version
	for range time.Tick(time.Second * 5) {
		n := l.v.Load().(vS).v
		if o.Less(n) {
			l.log.Info().
				Str("what", "notify").
				Str("old", n.String()).
				Str("new", n.String()).
				Msg("notifying about new version")
			for _, v := range l.n {
				go v.NotifyVersion(o, n)
			}
			o = n
		}
	}
}

func (l *LatestTracker) InterceptUpgradeCheck(buf []byte) {
	var s struct{ UpgradeURL, ReleaseNoteURL string }
	if err := json.Unmarshal(buf, &s); err == nil {
		if u := s.UpgradeURL; u != "" {
			v := MustExtractVersion(u)
			if cv := l.v.Load().(vS); cv.v.Less(v) {
				l.log.Info().
					Str("what", "intercept-version").
					Str("new", v.String()).
					Str("url", u).
					Msg("intercepted newer upgrade check version")
				l.v.Store(vS{v, u})
			}
		}
		if u := s.ReleaseNoteURL; u != "" {
			if x := strings.LastIndex(u, "/"); x != -1 {
				t, _ := strconv.ParseUint(u[x+1:], 10, 64)
				if ct := l.t.Load().(tS); ct.t < t {
					l.log.Info().
						Str("what", "intercept-notes").
						Uint64("new", t).
						Str("url", u).
						Msg("intercepted newer upgrade check notes")
					l.t.Store(tS{t, u})
				}
			}
		}
	}
}

func (l *LatestTracker) WritePrometheus(w io.Writer) {
	m := metrics.NewSet()
	if cv := l.v.Load().(vS); !cv.v.Zero() {
		m.NewGauge(`kfwproxy_latest_version{full="`+cv.v.String()+`"}`, func() float64 { return float64(int(cv.v[2])) })
	}
	if ct := l.t.Load().(tS); ct.t != 0 {
		m.NewGauge(`kfwproxy_latest_notes`, func() float64 { return float64(int(ct.t)) })
	}
	m.WritePrometheus(w)
}

func (l *LatestTracker) Mount(r *httprouter.Router) {
	r.GET("/latest/notes", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		fmt.Fprintf(w, "%d", l.t.Load().(tS).t)
	})

	r.GET("/latest/version", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		fmt.Fprintf(w, "%s", l.v.Load().(vS).v)
	})

	r.GET("/latest/version/svg", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		fn := func(p, d string) string {
			if v := r.URL.Query().Get(p); v != "" {
				return strings.ReplaceAll(v, `"`, `'`)
			}
			return d
		}
		fw := fn("fw", "72")
		fh := fn("fh", "12")
		ff := fn("ff", "Verdana, Arial, Helvetica, sans-serif")
		fc := fn("fc", "#000")

		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg" version="1.1" width="%s" height="%s"><text x="0" y="%s" font-size="%s" font-family="%s" fill="%s">%s</text><!--%s--></svg>`, fw, fh, fh, fh, ff, fc, l.v.Load().(vS).v, time.Now())
	})

	r.GET("/latest/version/png", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		font := pixfont.Font8x8
		v := l.v.Load().(vS).v.String()
		iw, ih := font.MeasureString(v), font.GetHeight()
		img := image.NewRGBA(image.Rect(0, 0, iw, ih))
		font.DrawString(img, 0, 0, v, color.Black)
		png.Encode(w, img)
	})

	r.GET("/latest/notes/redir", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		http.Redirect(w, r, l.t.Load().(tS).u, http.StatusTemporaryRedirect)
	})

	r.GET("/latest/version/redir", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		http.Redirect(w, r, l.v.Load().(vS).u, http.StatusTemporaryRedirect)
	})
}
