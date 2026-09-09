package model

import "testing"

// Dedupe hinges on these collapsing to one key: the same story reaches us from
// several feeds with tracking parameters, mixed scheme and an optional "www.".
func TestCanonicalURLCollapsesFeedVariantsOfTheSameStory(t *testing.T) {
	variants := []string{
		"https://www.example.com/markets/story-123",
		"http://example.com/markets/story-123",
		"https://example.com/markets/story-123/",
		"https://www.example.com/markets/story-123?utm_source=rss&utm_medium=feed",
		"https://EXAMPLE.com/markets/story-123#section",
		"  https://www.example.com/markets/story-123  ",
	}

	want := ArticleID(variants[0])
	for _, v := range variants[1:] {
		if got := ArticleID(v); got != want {
			t.Errorf("ArticleID(%q) = %s, want %s (same story, different feed)", v, got, want)
		}
	}
}

func TestCanonicalURLKeepsDistinctStoriesApart(t *testing.T) {
	distinct := []string{
		"https://example.com/markets/story-123",
		"https://example.com/markets/story-124",
		"https://other.com/markets/story-123",
	}

	seen := make(map[string]string, len(distinct))
	for _, u := range distinct {
		id := ArticleID(u)
		if prev, dup := seen[id]; dup {
			t.Errorf("%q and %q collided on ID %s", prev, u, id)
		}
		seen[id] = u
	}
}

func TestCanonicalURLHandlesMalformedInput(t *testing.T) {
	// Feeds occasionally carry junk in <link>. It must not panic, and must
	// still produce a stable key so the item can be deduped like any other.
	for _, in := range []string{"", "not a url", "://broken", "/relative/path"} {
		if got := ArticleID(in); got == "" {
			t.Errorf("ArticleID(%q) returned an empty ID", in)
		}
	}
}

func TestGroupIDSlugifies(t *testing.T) {
	tests := map[string]string{
		"Semiconductors & AI": "semiconductors-ai",
		"Big Tech":            "big-tech",
		"  Macro / Rates  ":   "macro-rates",
		"Energy":              "energy",
		"S&P 500":             "s-p-500",
		"!!!":                 "",
	}
	for in, want := range tests {
		if got := GroupID(in); got != want {
			t.Errorf("GroupID(%q) = %q, want %q", in, got, want)
		}
	}
}
