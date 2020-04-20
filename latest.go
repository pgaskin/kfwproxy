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
	"sync"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/pbnjay/pixfont"
)

type latestTracker struct {
	version    Version
	versionURL string
	notes      uint64
	notesURL   string
	versionMu  sync.RWMutex
	notesMu    sync.RWMutex
	notifier   []func(old, new Version)
}

func (l *latestTracker) Notify(fn func(old, new Version)) {
	if fn != nil {
		l.notifier = append(l.notifier, fn)
	}
}

func (l *latestTracker) WritePrometheus(w io.Writer) {
	l.versionMu.Lock()
	defer l.versionMu.Unlock()

	m := metrics.NewSet()
	if !l.version.Zero() {
		m.NewGauge("kfwproxy_latest_version{full=\""+l.version.String()+"\"}", func() float64 { return float64(int(l.version[2])) })
	}
	if l.notes != 0 {
		m.NewGauge("kfwproxy_latest_notes", func() float64 { return float64(int(l.notes)) })
	}
	m.WritePrometheus(w)
}

func (l *latestTracker) HandleVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%s", l.version)
}

func (l *latestTracker) HandleNotes(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%d", l.notes)
}

func (l *latestTracker) HandleVersionSVG(w http.ResponseWriter, r *http.Request) {
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
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg" version="1.1" width="%s" height="%s"><text x="0" y="%s" font-size="%s" font-family="%s" fill="%s">%s</text><!--%s--></svg>`, fw, fh, fh, fh, ff, fc, l.version, time.Now())
}

func (l *latestTracker) HandleVersionPNG(w http.ResponseWriter, r *http.Request) {
	l.versionMu.RLock()
	defer l.versionMu.RUnlock()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	font := pixfont.Font8x8
	iw, ih := font.MeasureString(l.version.String()), font.GetHeight()
	img := image.NewRGBA(image.Rect(0, 0, iw, ih))
	font.DrawString(img, 0, 0, l.version.String(), color.Black)
	png.Encode(w, img)
}

func (l *latestTracker) HandleVersionRedir(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, l.versionURL, http.StatusTemporaryRedirect)
}

func (l *latestTracker) HandleNotesRedir(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, l.notesURL, http.StatusTemporaryRedirect)
}

func (l *latestTracker) UpdateVersion(url string) {
	l.versionMu.Lock()
	defer l.versionMu.Unlock()
	if url != "" {
		new := MustExtractVersion(url)
		if old := l.version; old.Less(new) {
			l.version = new
			l.versionURL = url
			for _, fn := range l.notifier {
				go fn(old, new)
			}
		}
	}
}

func (l *latestTracker) UpdateNotes(url string) {
	l.notesMu.Lock()
	defer l.notesMu.Unlock()
	if url != "" {
		spl := strings.Split(url, "/")
		if len(spl) > 0 {
			if i, err := strconv.ParseUint(strings.Split(spl[len(spl)-1], "?")[0], 10, 64); err == nil && l.notes < i {
				l.notes = i
				l.notesURL = url
			}
		}
	}
}

func (l *latestTracker) InterceptUpgradeCheck(buf []byte) {
	var s struct{ UpgradeURL, ReleaseNoteURL string }
	_ = json.Unmarshal(buf, &s)
	l.UpdateVersion(s.UpgradeURL)
	l.UpdateNotes(s.ReleaseNoteURL)
}
