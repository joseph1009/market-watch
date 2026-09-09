package model

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
)

// Article is a single news item, normalized from whatever feed produced it.
type Article struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	SourceID   string    `json:"source_id"`
	SourceName string    `json:"source_name"`
	Summary    string    `json:"summary"`
	Published  time.Time `json:"published"`
	Fetched    time.Time `json:"fetched"`
	Tickers    []string  `json:"tickers,omitempty"`
	GroupIDs   []string  `json:"group_ids,omitempty"`
}

// InGroup reports whether the article was matched to the given watchlist group.
func (a Article) InGroup(groupID string) bool {
	for _, id := range a.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// ArticleID derives the dedupe key for an article URL. Feed GUIDs are not
// stable across refetches and the same story arrives with different tracking
// parameters, so the key hashes the canonical host and path instead.
func ArticleID(rawURL string) string {
	sum := sha256.Sum256([]byte(CanonicalURL(rawURL)))
	return hex.EncodeToString(sum[:16])
}

// CanonicalURL strips the parts of a URL that vary between feeds pointing at
// the same story: scheme, "www.", query string, fragment and trailing slash.
func CanonicalURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return strings.ToLower(trimmed)
	}
	u.Scheme = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	u.Host = strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	u.Path = strings.TrimSuffix(u.Path, "/")
	return strings.TrimPrefix(u.String(), "//")
}
