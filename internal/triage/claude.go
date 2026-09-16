package triage

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/report"
)

// maxTokens is several times what a batch needs: a verdict line is about a
// dozen tokens. A reply that still hits it is parsed as far as it got, the
// articles past that point stay unrated, and the batch is reported as failed.
const maxTokens = 2048

// Claude is the Anthropic-backed Completer.
type Claude struct {
	Client anthropic.Client
	Model  string
}

// NewClaude builds a client for the given key and model.
func NewClaude(apiKey, model string) *Claude {
	return &Claude{
		Client: anthropic.NewClient(option.WithAPIKey(apiKey)),
		Model:  model,
	}
}

// Complete rates one batch. No streaming, and thinking explicitly off: the reply
// is short and mechanical. Leaving thinking unset is not the same -- Sonnet 5
// thinks by default, spent the whole token budget on it, and cut every batch
// off partway through the verdicts.
func (c *Claude) Complete(ctx context.Context, system, prompt string) (string, model.Usage, error) {
	disabled := anthropic.NewThinkingConfigDisabledParam()
	msg, err := c.Client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.Model),
		MaxTokens: maxTokens,
		Thinking:  anthropic.ThinkingConfigParamUnion{OfDisabled: &disabled},
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return "", model.Usage{}, err
	}

	usage := model.Usage{
		InputTokens:  msg.Usage.InputTokens,
		OutputTokens: msg.Usage.OutputTokens,
	}
	usage.EstimatedUSD = report.EstimateCost(c.Model, usage)

	if msg.StopReason == anthropic.StopReasonRefusal {
		return "", usage, fmt.Errorf("model declined the batch")
	}

	var b strings.Builder
	for _, block := range msg.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(text.Text)
		}
	}
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		// The text is still returned: its verdicts are valid up to the cut.
		return b.String(), usage, fmt.Errorf("reply cut off at the %d token limit", maxTokens)
	}
	return b.String(), usage, nil
}
