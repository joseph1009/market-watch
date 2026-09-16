package report

import "github.com/joseph1009/market-watch/internal/model"

// rate is the per-million-token price of a model, in US dollars.
type rate struct {
	input  float64
	output float64
}

// prices are Anthropic first-party API rates, correct as of 2026-09-09. They
// are compiled in because the API does not quote a price with a response, so
// every figure derived from them is an estimate: if Anthropic changes rates,
// this table is what goes stale, and the Console remains the source of truth
// for real spend.
var prices = map[string]rate{
	"claude-opus-5":    {input: 5, output: 25},
	"claude-opus-4-8":  {input: 5, output: 25},
	"claude-sonnet-5":  {input: 2, output: 10},
	"claude-haiku-4-5": {input: 1, output: 5},
	"claude-fable-5-1": {input: 10, output: 50},
}

// EstimateCost prices a run. An unknown model yields zero rather than a wrong
// number -- a missing cost reads as "not known", where a fabricated one would
// be taken at face value.
func EstimateCost(modelID string, u model.Usage) float64 {
	r, known := prices[modelID]
	if !known {
		return 0
	}
	const perMillion = 1_000_000.0
	return (float64(u.InputTokens)*r.input + float64(u.OutputTokens)*r.output) / perMillion
}
