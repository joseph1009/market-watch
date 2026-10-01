package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/relay"
	"github.com/joseph1009/market-watch/internal/runcache"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// maxIndustryRunes bounds what a reader may ask about: a word or a few.
const maxIndustryRunes = 80

// handleIndustry answers /industry: how an industry fits together, part by
// part, and listed companies to look into in each, as a map for the
// company-by-company reading /analyse does.
func (a *App) handleIndustry(ctx context.Context, msg telegram.Message, args []string) (err error) {
	if a.Industry == nil {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, "Explaining industries is not configured on this instance.")
	}
	topic := strings.TrimSpace(strings.Join(args, " "))
	if topic == "" {
		return a.Bot.SendMessage(ctx, msg.Chat.ID,
			"Which industry? Send a word or two, for example /industry robotics, /industry AI or /industry automobiles.\n\n"+
				"I explain how it fits together -- its building blocks from raw materials to the customer, how each part makes money and who leads it -- "+
				"and suggest listed companies to look into in each part. Send /analyse with any of their tickers to read one's accounts.")
	}
	topic = clipRunes(topic, maxIndustryRunes)

	ctx, cached := a.Cache.Start(ctx, runcache.Industry, topic)
	defer func() { cached.Finish(err) }()
	if err := a.Bot.SendMessage(ctx, msg.Chat.ID,
		fmt.Sprintf("Mapping %s: its parts, who leads each, and companies to look into. A few minutes.", escape(topic))); err != nil {
		return err
	}

	// Its own budget, as /analyse has: one long call that searches the web.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute+a.Cfg.CallTimeout)
	defer cancel()
	if a.Relay != nil {
		var run *relay.Run
		if ctx, run, err = a.Relay.Begin(ctx, "industry"); err != nil {
			return err
		}
		a.Log.Info("relay run", "dir", run.Dir)
	}

	exp, err := a.Industry.Explain(ctx, topic)
	if err != nil {
		return err
	}
	cached.Save("explanation", exp)
	companies := make([]telegram.IndustryCompany, len(exp.Companies))
	for i, c := range exp.Companies {
		companies[i] = telegram.IndustryCompany{Part: c.Part, Name: c.Name, Symbol: c.Symbol(), Why: c.Why}
	}

	messages := telegram.RenderIndustry(exp.Topic, exp.Text, companies, telegram.IdeasOptions{}, a.Terms)
	cached.Text("messages.html", joinMessages(messages))
	if _, err := a.Bot.SendReport(ctx, msg.Chat.ID, messages); err != nil {
		return err
	}
	// /share posts the channel's copy, which says a model wrote it.
	a.rememberFor("the "+exp.Topic+" industry", messages,
		telegram.RenderIndustry(exp.Topic, exp.Text, companies, telegram.IdeasOptions{ForChannel: true}, a.Terms))
	a.Log.Info("industry explained", "topic", exp.Topic, "companies", len(companies),
		"input_tokens", exp.Usage.InputTokens, "output_tokens", exp.Usage.OutputTokens)
	return nil
}
