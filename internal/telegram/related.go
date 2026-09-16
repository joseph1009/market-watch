package telegram

import (
	"strings"
)

// Related is one company to read beside the analysed one. The telegram package
// keeps its own shape so it does not import the analysis package: this is a
// rendering concern, and the fields are what a message needs.
type Related struct {
	Name   string
	Symbol string
	Listed string
	Why    string
}

// RenderRelated lists the companies worth looking at next to the one analysed.
//
// Its own message, at the end: it is a list of things to go and do, not part of
// the reading, and a reader returning to it later should find it in one place
// rather than at the foot of a wall of analysis.
func RenderRelated(related []Related) string {
	if len(related) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<b>Companies to read next to it</b>\n")
	b.WriteString("<i>Every ticker checked against its exchange. Not recommendations.</i>")
	for _, r := range related {
		b.WriteString("\n\n• <b>" + escape(r.Name) + "</b>")
		if r.Symbol != "" {
			b.WriteString(" <code>" + escape(r.Symbol) + "</code>")
		}
		b.WriteString("\n" + escape(r.Why))
	}
	return b.String()
}

// RelatedList converts any shape carrying the same four fields, so the caller
// does not have to build this package's type by hand.
func RelatedList[T any](items []T, fields func(T) (name, symbol, listed, why string)) []Related {
	out := make([]Related, 0, len(items))
	for _, item := range items {
		name, symbol, listed, why := fields(item)
		out = append(out, Related{Name: name, Symbol: symbol, Listed: listed, Why: why})
	}
	return out
}
