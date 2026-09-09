package report

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// defaultMaxTokens is generous on purpose. A brief runs well under this, and
// hitting the cap truncates mid-sentence, which is treated as a failed run
// rather than sent to the reader.
const defaultMaxTokens = 16000

// Claude is the Anthropic-backed Completer.
type Claude struct {
	Client    anthropic.Client
	Model     string
	MaxTokens int64
}

// NewClaude builds a client for the given key and model.
func NewClaude(apiKey, model string) *Claude {
	return &Claude{
		Client:    anthropic.NewClient(option.WithAPIKey(apiKey)),
		Model:     model,
		MaxTokens: defaultMaxTokens,
	}
}

// Complete asks the model for one brief.
//
// The request streams: a full day of articles is a long input, and streaming
// keeps a slow generation from tripping the client's request timeout. Adaptive
// thinking is on -- deciding what actually mattered in a day of market news is
// exactly the kind of judgment it helps with -- and the reasoning itself is not
// requested back, since only the brief is ever shown.
func (c *Claude) Complete(ctx context.Context, system, prompt string) (string, error) {
	adaptive := anthropic.ThinkingConfigAdaptiveParam{}

	stream := c.Client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.Model),
		MaxTokens: c.maxTokens(),
		System:    []anthropic.TextBlockParam{{Text: system}},
		Thinking:  anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})

	var message anthropic.Message
	for stream.Next() {
		if err := message.Accumulate(stream.Current()); err != nil {
			return "", fmt.Errorf("accumulate response: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return "", err
	}

	switch message.StopReason {
	case anthropic.StopReasonRefusal:
		// Surfaced rather than retried: a refusal on market news means the input
		// was not what we think it is, and a second attempt would not fix that.
		return "", fmt.Errorf("model declined the request (%s): %s",
			message.StopDetails.Category, message.StopDetails.Explanation)
	case anthropic.StopReasonMaxTokens:
		return "", fmt.Errorf("response hit the %d token cap and would be cut mid-sentence", c.maxTokens())
	}

	var b strings.Builder
	for _, block := range message.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(text.Text)
		}
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("model returned no text (stop reason %q)", message.StopReason)
	}
	return out, nil
}

func (c *Claude) maxTokens() int64 {
	if c.MaxTokens > 0 {
		return c.MaxTokens
	}
	return defaultMaxTokens
}
