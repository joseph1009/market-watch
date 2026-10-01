// Package pages keeps the service's messages as web pages, so a chat can be
// sent a short summary and a button to the whole thing.
//
// A brief is a dozen messages and an analysis half as many again; read on a
// phone, that is a long scroll through a chat. As a page it reads as one
// document, with room for tables, folded-away sources and a layout Telegram's
// few tags cannot draw. The chat keeps what fits on one screen.
//
// The pages are served by the service itself, from the data volume. Each has
// a random 128-bit address, so one cannot be found except by being given it,
// and the server answers nothing but those addresses: no index, no listing,
// no commands. Pages are deleted after a month.
package pages

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultKeep is how long a page stays readable.
const DefaultKeep = 30 * 24 * time.Hour

// Page is one message's full copy: laid out as a document where there is
// one, otherwise from the messages as Telegram would show them.
type Page struct {
	Title       string // the browser tab's, and a shared link's, title
	Description string // plain text, for a shared link's preview
	Doc         *Doc
	Messages    []string
	Note        string // optional closing line under messages, Telegram HTML
}

// Store writes pages to a directory and serves them.
type Store struct {
	Dir     string
	BaseURL string // https://example.fly.dev, with no trailing slash
	Keep    time.Duration
	Now     func() time.Time
}

// Publish saves a page and returns its address. Pages older than Keep are
// deleted on the way, so the directory never needs tending.
func (s *Store) Publish(p Page) (string, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", fmt.Errorf("pages directory: %w", err)
	}
	s.prune()

	id, err := newID()
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.Dir, id+".html")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, Render(p), 0o644); err != nil {
		return "", fmt.Errorf("write page: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("write page: %w", err)
	}
	return s.BaseURL + "/r/" + id, nil
}

func (s *Store) prune() {
	keep := s.Keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || now.Sub(info.ModTime()) <= keep {
			continue
		}
		os.Remove(filepath.Join(s.Dir, e.Name()))
	}
}

// newID is 128 random bits, which nobody will guess.
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("page id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var pagePath = regexp.MustCompile(`^/r/([A-Za-z0-9_-]{22})$`)

// Handler answers GET /r/<id> with that page and everything else with "not
// found". It reads, and never writes.
func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Robots-Tag", "noindex, nofollow")

		if r.URL.Path == "/robots.txt" {
			h.Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
			return
		}
		m := pagePath.FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := os.ReadFile(filepath.Join(s.Dir, m[1]+".html"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// No scripts, nothing fetched from elsewhere, and no framing.
		h.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "private, max-age=300")
		w.Write(body)
	})
}

// Serve runs the page server on addr until ctx ends.
func (s *Store) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("pages: %w", err)
	}
	return ctx.Err()
}

// Plain strips Telegram HTML to text, for a page's description.
func Plain(html string) string {
	text := tag.ReplaceAllString(html, "")
	text = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&amp;", "&").Replace(text)
	return strings.Join(strings.Fields(text), " ")
}
