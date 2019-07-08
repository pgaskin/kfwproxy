package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pbnjay/pixfont"
)

type latestTracker struct {
	version    version
	versionURL string
	notes      uint64
	notesURL   string
	versionMu  sync.RWMutex
	notesMu    sync.RWMutex
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
		v := extractVersion(url)
		if l.version.Less(v) {
			l.version = v
			l.versionURL = url
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

type version [3]uint64

var versionRe = regexp.MustCompile(`([0-9]+)\.([0-9]+)(?:\.([0-9]+))?`)

func extractVersion(str string) version {
	m := versionRe.FindStringSubmatch(str)
	var v version
	var err error
	for i := range v {
		if i+1 < len(m) && m[i+1] != "" {
			v[i], err = strconv.ParseUint(m[i+1], 10, 64)
			if err != nil {
				panic(err)
			}
		}
	}
	return v
}

func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

func (v version) Less(w version) bool {
	return !(v[0] > w[0] || (v[0] == w[0] && (v[1] > w[1] || (v[1] == w[1] && (v[2] > w[2] || v[2] == w[2])))))
}

func (v version) Equal(w version) bool {
	return v[0] == w[0] && v[1] == w[1] && v[2] == w[2]
}
