package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/industry"
	"github.com/joseph1009/market-watch/internal/model"
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
		return a.ask(ctx, msg.Chat.ID, "industry",
			"🧭 <b>Which industry?</b> Send me a word or two, for example robotics, AI or automobiles. "+
				"Add -m and a market to keep the companies to its listings, for example AI -m US.\n\n"+
				"I explain how it fits together -- its building blocks from raw materials to the customer, how each part makes money and who leads it -- "+
				"and suggest listed companies to look into in each part. Send /analyse with any of their tickers to read one's accounts.",
			"Industry, e.g. robotics or AI -m US")
	}
	// "-m US" keeps the companies to that market's listings; without it they
	// come from anywhere.
	topic, market, err := industry.ParseMarket(topic)
	if err != nil {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, escape(err.Error()))
	}
	if topic == "" {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, "Which industry? Send it before the market, for example /industry AI -m US.")
	}
	topic = clipRunes(topic, maxIndustryRunes)

	ctx, cached := a.Cache.Start(ctx, runcache.Industry, topic)
	defer func() { cached.Finish(err) }()
	if err := a.Bot.SendMessage(ctx, msg.Chat.ID,
		fmt.Sprintf("Mapping %s: its parts, who leads each, and companies to look into%s. A few minutes.", escape(topic), escape(listedNote(market)))); err != nil {
		return err
	}

	// Its own budget, as /analyse has: one long call that searches the web.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute+a.Cfg.CallTimeout)
	defer cancel()
	if a.Relay != nil {
		var run *relay.Run
		if ctx, run, err = a.Relay.Begin(ctx, "industry-"+topic); err != nil {
			return err
		}
		a.Log.Info("relay run", "dir", run.Dir)
	}

	exp, err := a.Industry.Explain(ctx, topic, market)
	if err != nil {
		return err
	}
	// The title says where the companies are listed, where the reader chose.
	shown := exp.Topic
	if exp.Market != "" {
		shown += " (" + industry.MarketLabel(exp.Market) + "-listed)"
	}
	cached.Save("explanation", exp)
	companies := make([]telegram.IndustryCompany, len(exp.Companies))
	for i, c := range exp.Companies {
		companies[i] = telegram.IndustryCompany{Part: c.Part, Name: c.Name, Symbol: c.Symbol(), Why: c.Why, Exchange: c.Exchange, Market: model.MarketOf(c.Exchange)}
	}

	industryFor := func(opts telegram.IdeasOptions) outgoing {
		doc := telegram.IndustryDoc(shown, exp.Text, companies, sourceLinks(exp.Sources), opts, a.knownTerms())
		return outgoing{
			title:    capitalise(shown) + " · how the industry fits together",
			messages: telegram.RenderIndustry(shown, exp.Text, companies, sourceLinks(exp.Sources), opts, a.knownTerms()),
			summary:  telegram.IndustrySummary(shown, exp.Text, companies, opts),
			doc:      &doc,
		}
	}
	owner := industryFor(telegram.IdeasOptions{})
	cached.Text("messages.html", joinMessages(owner.messages))
	cached.Text("summary.html", owner.summary.Text)
	if _, err := a.send(ctx, msg.Chat.ID, owner, false); err != nil {
		return err
	}
	// /share posts the channel's copy, which says a model wrote it.
	// Only what the owner was sent: /share passes on the owner's latest.
	if msg.Chat.ID == a.Prefs().ChatID {
		channel := industryFor(telegram.IdeasOptions{ForChannel: true})
		a.rememberSent("the "+shown+" industry", owner, &channel)
	}
	a.Log.Info("industry explained", "topic", shown, "companies", len(companies),
		"input_tokens", exp.Usage.InputTokens, "output_tokens", exp.Usage.OutputTokens)
	return nil
}

// listedNote is what the first reply says of a chosen market: ", listed in
// the United States only".
func listedNote(market string) string {
	if m, ok := model.Markets[market]; ok {
		return ", listed in " + theCountry(m.Country) + " only"
	}
	return ""
}

// theCountry puts "the" before the countries that take it.
func theCountry(country string) string {
	switch country {
	case "United States", "United Kingdom", "Netherlands":
		return "the " + country
	}
	return country
}
