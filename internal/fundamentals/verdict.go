package fundamentals

import (
	"strings"
)

// VerdictMarker heads the analysis's last section of prose: its view on the
// stock, BUY, HOLD or SELL against the S&P 500 over twelve months.
const VerdictMarker = "THE VERDICT"

// Verdict is the analysis's view on the stock, as written.
type Verdict struct {
	// Verdict is BUY, HOLD or SELL, and Confidence low, medium or high;
	// either is empty where the reply did not give one in that form.
	Verdict    string
	Confidence string

	// Body is the rest of the section: the sub-headings and bullets saying
	// why, and what would change it.
	Body string
}

// SplitVerdict takes the verdict section out of the analysis prose, so it
// can be shown under its own heading and written to the scorecard. The prose
// comes back without it. Where there is no section, or no verdict in it that
// reads as one, the prose comes back whole and the verdict empty.
func SplitVerdict(prose string) (string, Verdict) {
	lines := strings.Split(prose, "\n")
	start := -1
	for i, line := range lines {
		if strings.Trim(strings.TrimSpace(line), "#*: ") == VerdictMarker {
			start = i
			break
		}
	}
	if start < 0 {
		return prose, Verdict{}
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if heading(lines[i]) {
			end = i
			break
		}
	}

	var v Verdict
	var body []string
	for _, line := range lines[start+1 : end] {
		// Bold, where the model added it, is dropped: "**VERDICT:** Sell."
		trimmed := strings.TrimSpace(strings.ReplaceAll(line, "*", ""))
		if value, ok := field(trimmed, "VERDICT:"); ok && v.Verdict == "" {
			v.Verdict = oneOf(value, "BUY", "HOLD", "SELL")
			continue
		}
		if value, ok := field(trimmed, "CONFIDENCE:"); ok && v.Confidence == "" {
			v.Confidence = strings.ToLower(oneOf(value, "LOW", "MEDIUM", "HIGH"))
			continue
		}
		body = append(body, line)
	}
	if v.Verdict == "" {
		return prose, Verdict{}
	}
	v.Body = strings.TrimSpace(strings.Join(body, "\n"))

	rest := append(append([]string{}, lines[:start]...), lines[end:]...)
	return strings.TrimSpace(strings.Join(rest, "\n")), v
}

// field reads "LABEL: value" in any case.
func field(line, label string) (string, bool) {
	if len(line) < len(label) || !strings.EqualFold(line[:len(label)], label) {
		return "", false
	}
	return strings.TrimSpace(line[len(label):]), true
}

// oneOf is the first word of s in capitals where it is one of allowed, and
// empty otherwise: "BUY." and "Buy -- clearly" both read as BUY.
func oneOf(s string, allowed ...string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	first := strings.ToUpper(strings.Trim(words[0], ".,;:*-–—"))
	for _, a := range allowed {
		if first == a {
			return a
		}
	}
	return ""
}

// heading reports whether a line opens a section of the analysis: a short
// line in capitals, as the prompt asks the sections to be headed.
func heading(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || len(line) > 60 || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "#") {
		return false
	}
	letters := 0
	for _, r := range line {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= 'A' && r <= 'Z':
			letters++
		}
	}
	// "VERDICT: BUY" is a field inside the section, not a heading after it.
	return letters >= 3 && !strings.Contains(line, ":")
}
