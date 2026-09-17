package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joseph1009/market-watch/internal/config"
	"github.com/joseph1009/market-watch/internal/fundamentals"
	"github.com/joseph1009/market-watch/internal/logging"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/telegram"
)

const helpText = `<b>📊 Market Watch</b>

/now — build and send a brief right now
/analyse &lt;ticker&gt; — analyse a company from its filings, e.g. /analyse NVDA
/watchlist — show your watchlists
/watchlist add &lt;group&gt; &lt;ticker or name&gt; — track something
/watchlist remove &lt;group&gt; &lt;ticker or name&gt; — stop tracking it
/sources — show the news feeds
/sources on|off &lt;id&gt; — enable or disable a feed
/schedule — when the next brief is due
/stats — what recent briefs cost and did
/clear — remove the bot's earlier messages from this chat
/help — this message

The daily brief arrives on its own; these are for when you want one early, or want to change what it covers.`

// BotCommands is the menu Telegram shows when someone types "/". It mirrors
// helpText, and both have to be updated together -- the menu is how anyone
// discovers the commands exist at all.
func BotCommands() []telegram.Command {
	return []telegram.Command{
		{Command: "now", Description: "Build and send a brief right now"},
		{Command: "watchlist", Description: "Show or edit your watchlists"},
		{Command: "analyse", Description: "Analyse a company from its filings: /analyse NVDA"},
		{Command: "sources", Description: "Show or toggle the news feeds"},
		{Command: "schedule", Description: "When the next brief is due"},
		{Command: "stats", Description: "What recent briefs cost and did"},
		{Command: "clear", Description: "Remove my earlier messages from this chat"},
		{Command: "help", Description: "What I can do"},
	}
}

// HandleMessage routes one incoming message. Failures are reported into the
// chat rather than returned: the sender is the only person who can act on them,
// and the polling loop has to keep running either way.
func (a *App) HandleMessage(ctx context.Context, msg telegram.Message) {
	command, args := splitCommand(msg.Text)
	if command == "" {
		return // ordinary chatter, not addressed to the bot
	}

	a.Log.Info("command", "command", command, "chat", msg.Chat.ID)

	var err error
	switch command {
	case "start":
		err = a.handleStart(ctx, msg)
	case "help":
		err = a.Bot.SendMessage(ctx, msg.Chat.ID, helpText)
	case "now":
		err = a.handleNow(ctx, msg)
	// Spelling and the earlier name both route here: a command that answers
	// only to one spelling reads as broken to whoever typed the other.
	case "analyse", "analyze", "accounts":
		err = a.handleAnalyse(ctx, msg, args)
	case "watchlist":
		err = a.handleWatchlist(ctx, msg, args)
	case "sources":
		err = a.handleSources(ctx, msg, args)
	case "schedule":
		err = a.handleSchedule(ctx, msg)
	case "stats":
		err = a.handleStats(ctx, msg)
	case "clear":
		err = a.handleClear(ctx, msg)
	default:
		err = a.Bot.SendMessage(ctx, msg.Chat.ID,
			fmt.Sprintf("Unknown command %s. Try /help.", escape("/"+command)))
	}

	if err != nil {
		a.Log.Error("command failed", "command", command, "error", err)
		// The reply is a second route out for an error's text, and errors are
		// where a credential ends up. The logger scrubs its own output; this
		// path has to scrub its own.
		clean := logging.Scrub(err.Error(), a.Cfg.TelegramBotToken, a.Cfg.AnthropicAPIKey)
		_ = a.Bot.SendMessage(ctx, msg.Chat.ID, "Something went wrong: "+escape(clean))
	}
}

// splitCommand parses "/watchlist add semis-ai NVDA" into its parts. Telegram
// appends "@botname" to commands used in a group chat.
func splitCommand(text string) (command string, args []string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", nil
	}

	command = strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if at := strings.IndexByte(command, '@'); at >= 0 {
		command = command[:at]
	}
	return command, fields[1:]
}

// handleStart records where to deliver. This is the whole reason the chat id
// need never be configured by hand.
func (a *App) handleStart(ctx context.Context, msg telegram.Message) error {
	if a.Prefs().ChatID == msg.Chat.ID {
		return a.Bot.SendMessage(ctx, msg.Chat.ID,
			"Already set up — the daily brief comes here.\n\nSend /help to see what else I can do.")
	}

	if err := a.UpdatePrefs(func(p *config.Prefs) error {
		p.ChatID = msg.Chat.ID
		return nil
	}); err != nil {
		return err
	}

	a.Log.Info("chat registered", "chat", msg.Chat.ID)
	when := a.Cfg.NextRun(a.now()).In(a.Cfg.DisplayLocation)
	return a.Bot.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf(
		"<b>📊 Market Watch</b>\n\nSet up. The daily brief will arrive here, next at %s.\n\nSend /now for one immediately, or /help for everything else.",
		escape(when.Format("Mon 2 Jan at 15:04 MST"))))
}

func (a *App) handleNow(ctx context.Context, msg telegram.Message) error {
	// Collecting and writing takes about a minute. Saying so beats silence and
	// confirms the command was heard.
	if err := a.Bot.SendMessage(ctx, msg.Chat.ID, "Collecting and writing your brief — about a minute."); err != nil {
		return err
	}

	// Asking for a brief in a chat the bot does not know is an implicit /start.
	if a.Prefs().ChatID == 0 {
		if err := a.UpdatePrefs(func(p *config.Prefs) error {
			p.ChatID = msg.Chat.ID
			return nil
		}); err != nil {
			return err
		}
	}
	return a.SendReport(ctx)
}

// DefaultSweepWindow is how many message ids back /clear reaches. Roughly a
// week of briefs at nine messages each, plus the replies in between.
const DefaultSweepWindow = 300

// handleClear removes the bot's earlier messages from the chat.
//
// This exists because recording message ids only helps for briefs sent after
// the recording started: everything before that is unreachable by id, and the
// Bot API offers no way to list what a bot has sent. Walking backwards from a
// known id is the only route to the backlog.
func (a *App) handleClear(ctx context.Context, msg telegram.Message) error {
	return a.ClearChat(ctx, msg.Chat.ID)
}

// ClearChat removes the bot's earlier messages. Exported so it can also be run
// from the command line, which is the state the service is usually in while
// someone is iterating on the brief.
func (a *App) ClearChat(ctx context.Context, chatID int64) error {
	// Sending first serves two purposes: it tells the reader something is
	// happening, and its id is the anchor to sweep backwards from.
	anchor, err := a.Bot.Send(ctx, chatID, "Clearing earlier messages — this takes a moment.")
	if err != nil {
		return err
	}

	deleted, skipped := a.Bot.SweepMessages(ctx, chatID, anchor-1, DefaultSweepWindow)
	// "skipped" rather than "failed": nearly every id in the window was never a
	// bot message -- the reader's own messages, ids that no longer exist -- so
	// the count describes the sweep, not what remains in the chat.
	a.Log.Info("swept chat", "chat", chatID, "deleted", deleted, "skipped_ids", skipped)

	if err := a.UpdatePrefs(func(p *config.Prefs) error {
		p.LastBrief = nil
		return nil
	}); err != nil {
		return err
	}

	// The reply reports only what was removed. It once also reported the ids
	// that could not be deleted, and "136 could not be removed" read as 136
	// messages still lingering in the chat -- when almost none of those ids
	// were ever messages the bot could have deleted. The sweep cannot tell a
	// too-old brief apart from the reader's own message, so it offers no count
	// of what remains, only the rule that decides it.
	reply := "Nothing of mine left to clear."
	if deleted > 0 {
		reply = fmt.Sprintf("Cleared %d message(s).", deleted)
	}
	reply += " A bot can only delete its messages within 48 hours, so anything older has to be removed by hand."
	_, err = a.Bot.Send(ctx, chatID, reply)
	return err
}

func (a *App) handleSchedule(ctx context.Context, msg telegram.Message) error {
	next := a.Cfg.NextRun(a.now())
	return a.Bot.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf(
		"<b>Schedule</b>\nDaily at %s %s, which is %s where you are.\n\nNext: %s, in %s.",
		escape(a.Cfg.ReportAt.String()),
		escape(a.Cfg.ScheduleLocation.String()),
		escape(next.In(a.Cfg.DisplayLocation).Format("15:04 MST")),
		escape(next.In(a.Cfg.DisplayLocation).Format("Mon 2 Jan, 15:04")),
		escape(next.Sub(a.now()).Round(time.Minute).String())))
}

func (a *App) handleWatchlist(ctx context.Context, msg telegram.Message, args []string) error {
	if len(args) == 0 {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, renderWatchlists(a.Prefs().Groups))
	}

	action := strings.ToLower(args[0])
	if len(args) < 3 || (action != "add" && action != "remove") {
		return a.Bot.SendMessage(ctx, msg.Chat.ID,
			"Usage: /watchlist add &lt;group&gt; &lt;ticker or name&gt;\n\nGroups: "+escape(groupIDs(a.Prefs().Groups)))
	}

	groupID := model.GroupID(args[1])
	term := strings.Join(args[2:], " ")

	var outcome string
	err := a.UpdatePrefs(func(p *config.Prefs) error {
		for i := range p.Groups {
			if p.Groups[i].ID != groupID {
				continue
			}
			if action == "add" {
				outcome = addTerm(&p.Groups[i], term)
			} else {
				outcome = removeTerm(&p.Groups[i], term)
			}
			return nil
		}
		return fmt.Errorf("no watchlist called %q; try one of: %s", groupID, groupIDs(p.Groups))
	})
	if err != nil {
		return err
	}
	return a.Bot.SendMessage(ctx, msg.Chat.ID, escape(outcome))
}

// addTerm decides from its shape whether the term is a ticker or a company
// name. The two are matched differently -- symbols case-sensitively so "ARM"
// does not match an arm, names case-insensitively so "Nvidia" matches "nvidia".
func addTerm(g *model.Group, term string) string {
	if isTicker(term) {
		symbol := strings.ToUpper(term)
		for _, t := range g.Tickers {
			if t == symbol {
				return fmt.Sprintf("%s already tracks %s.", g.Name, symbol)
			}
		}
		g.Tickers = append(g.Tickers, symbol)
		return fmt.Sprintf("Added %s to %s.", symbol, g.Name)
	}

	for _, n := range g.Names {
		if strings.EqualFold(n, term) {
			return fmt.Sprintf("%s already tracks %s.", g.Name, term)
		}
	}
	g.Names = append(g.Names, term)
	return fmt.Sprintf("Added %s to %s as a name.", term, g.Name)
}

func removeTerm(g *model.Group, term string) string {
	before := len(g.Tickers) + len(g.Names) + len(g.Keywords)
	g.Tickers = withoutFold(g.Tickers, term)
	g.Names = withoutFold(g.Names, term)
	g.Keywords = withoutFold(g.Keywords, term)

	if before == len(g.Tickers)+len(g.Names)+len(g.Keywords) {
		return fmt.Sprintf("%s was not tracking %s.", g.Name, term)
	}
	return fmt.Sprintf("Removed %s from %s.", term, g.Name)
}

func withoutFold(list []string, term string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if !strings.EqualFold(v, term) {
			out = append(out, v)
		}
	}
	return out
}

// isTicker treats a short all-caps token as a symbol. Deliberately narrow: a
// wrong guess only decides which matcher runs, and a name is the safer default
// because case-insensitive matching finds more.
func isTicker(s string) bool {
	if s == "" || len(s) > 5 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func (a *App) handleSources(ctx context.Context, msg telegram.Message, args []string) error {
	if len(args) == 0 {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, renderSources(a.Prefs().Sources))
	}

	action := strings.ToLower(args[0])
	if len(args) != 2 || (action != "on" && action != "off") {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, "Usage: /sources on|off &lt;id&gt;")
	}

	id := args[1]
	var outcome string
	err := a.UpdatePrefs(func(p *config.Prefs) error {
		for i := range p.Sources {
			if p.Sources[i].ID != id {
				continue
			}
			p.Sources[i].Enabled = action == "on"
			state := "off"
			if p.Sources[i].Enabled {
				state = "on"
			}
			outcome = fmt.Sprintf("%s is now %s.", p.Sources[i].Name, state)
			return nil
		}
		return fmt.Errorf("no source called %q; send /sources to list them", id)
	})
	if err != nil {
		return err
	}
	return a.Bot.SendMessage(ctx, msg.Chat.ID, escape(outcome))
}

func renderWatchlists(groups []model.Group) string {
	if len(groups) == 0 {
		return "No watchlists configured."
	}

	var b strings.Builder
	b.WriteString("<b>Watchlists</b>\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "\n<b>%s</b> <i>(%s)</i>\n", escape(g.Name), escape(g.ID))
		if len(g.Tickers) > 0 {
			fmt.Fprintf(&b, "%s\n", escape(strings.Join(g.Tickers, " ")))
		}
		if len(g.Names) > 0 {
			fmt.Fprintf(&b, "<i>%s</i>\n", escape(strings.Join(g.Names, ", ")))
		}
		if len(g.Keywords) > 0 {
			fmt.Fprintf(&b, "<i>%d keywords</i>\n", len(g.Keywords))
		}
	}
	b.WriteString("\n<i>Change with /watchlist add|remove &lt;group&gt; &lt;term&gt;</i>")
	return b.String()
}

func renderSources(sources []model.Source) string {
	if len(sources) == 0 {
		return "No sources configured."
	}

	var b strings.Builder
	b.WriteString("<b>Sources</b>\n")
	for _, s := range sources {
		mark := "○"
		if s.Enabled {
			mark = "●"
		}
		fmt.Fprintf(&b, "\n%s <b>%s</b> <i>(%s, weight %d)</i>", mark, escape(s.Name), escape(s.ID), s.Weight)
	}
	b.WriteString("\n\n<i>● on, ○ off — change with /sources on|off &lt;id&gt;</i>")
	return b.String()
}

func groupIDs(groups []model.Group) string {
	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	return strings.Join(ids, ", ")
}

// escape mirrors the renderer's escaping for text assembled here. Watchlist
// terms come from the user, so they are markup until proven otherwise.
var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escape(s string) string { return escaper.Replace(s) }

// handleAnalyse reads one company's filed figures and writes them up.
//
// Separate from the daily brief on purpose: the brief reports what happened
// today, and this answers a different question -- what the accounts say about a
// company, whenever you happen to ask.
func (a *App) handleAnalyse(ctx context.Context, msg telegram.Message, args []string) error {
	if a.Accounts == nil || (a.Analyzer == nil && a.Agent == nil) {
		return a.Bot.SendMessage(ctx, msg.Chat.ID,
			"Reading filings is not configured on this instance.")
	}
	if len(args) == 0 {
		return a.Bot.SendMessage(ctx, msg.Chat.ID,
			"Which company? Send a ticker, for example /analyse NVDA.\n\n"+
				"I read what the company filed with the SEC — revenue, margins, cash and the balance sheet — then what the share has been doing and what has been written about it lately. "+
				"Any SEC filer works, including foreign companies with a US listing such as TSM or BABA. "+
				"It is a reading of the accounts, never advice on the stock.")
	}

	ticker := strings.ToUpper(strings.TrimSpace(args[0]))
	if err := a.Bot.SendMessage(ctx, msg.Chat.ID,
		fmt.Sprintf("Reading %s's filings — about a minute.", escape(ticker))); err != nil {
		return err
	}

	// Its own budget: the SEC reads are quick, the writing is not, and this
	// must not inherit however long the caller's context happens to have left.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	snapshot, err := a.Accounts.Fetch(ctx, ticker, accountYears)
	if err == nil {
		// The price is what turns filed figures into multiples. It is fetched
		// after the filings so a quote outage costs the valuation block and
		// never the analysis.
		snapshot.Price = a.quoteFor(ctx, ticker)

		// What the company does, how the share has traded, and what has been
		// written about it. All best-effort: the accounts are the part that
		// cannot be had anywhere else, and a failed fetch here must not cost
		// them.
		for _, problem := range fundamentals.AddBusiness(ctx, a.Filings, &snapshot, a.now()) {
			a.Log.Warn("analysis context", "ticker", ticker, "error", problem)
		}
		snapshot.Trading = a.tradingFor(ctx, ticker)
		if err := a.addNews(ctx, &snapshot); err != nil {
			a.Log.Warn("analysis news", "ticker", ticker, "error", err)
		}
	}
	if err != nil {
		a.Log.Warn("accounts", "ticker", ticker, "error", err)
		return a.Bot.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf(
			"I could not read %s. Either it does not file with the SEC — foreign listings and private companies mostly do not — or the ticker is wrong.",
			escape(ticker)))
	}

	analysis, err := a.analyse(ctx, snapshot)
	if err != nil {
		return err
	}
	a.Log.Info("analysed",
		"ticker", ticker,
		"years", len(snapshot.Years),
		"missing", len(snapshot.Missing),
		"estimated_usd", fmt.Sprintf("%.4f", analysis.Usage.EstimatedUSD))

	// The related list is cut out of the prose and checked before it is shown:
	// it is written as a pipe-delimited table, which reads badly in a chat, and
	// its tickers are the part of the analysis a reader is most likely to act on.
	prose, related := fundamentals.SplitRelated(analysis.Text)
	if a.Finder != nil {
		related = fundamentals.VerifyRelated(ctx, a.Finder.Verifier, related)
	} else {
		related = nil
	}

	heading := fmt.Sprintf("%s — what the filings say", snapshot.Ticker)
	messages := telegram.RenderPlain(heading, prose)
	shown := telegram.RelatedList(related, func(r fundamentals.Related) (string, string, string, string) {
		return r.Name, r.Symbol(), r.Listed, r.Why
	})
	if block := telegram.RenderRelated(shown); block != "" {
		messages = append(messages, block)
	}
	messages = append(messages, fmt.Sprintf(
		"<i>%s, from filings up to %s · ~$%.2f</i>",
		escape(snapshot.Company),
		escape(snapshot.Balance.AsOf.Format("2 Jan 2006")),
		analysis.Usage.EstimatedUSD))

	_, err = a.Bot.SendReport(ctx, msg.Chat.ID, messages)
	return err
}

// accountYears is how much history the analysis gets. Five years covers a cycle
// without burying the recent trend in a wall of columns.
const accountYears = 5

// analyse writes up a snapshot, preferring the agent.
//
// The agent looks up whatever this company's figures turn out to require --
// receivables against revenue, a prior year of inventory, what the free cash
// flow was spent on -- which is most of what makes the analysis about this
// company rather than about companies in general. It costs several times a
// single call, so a failure falls back to the fixed table rather than leaving
// the reader with nothing.
func (a *App) analyse(ctx context.Context, snapshot fundamentals.Snapshot) (fundamentals.Analysis, error) {
	if a.Agent != nil {
		analysis, err := a.Agent.Analyze(ctx, snapshot)
		if err == nil {
			return analysis, nil
		}
		a.Log.Warn("analysis agent failed; falling back to the fixed table",
			"ticker", snapshot.Ticker, "error", err)
		if a.Analyzer == nil {
			return fundamentals.Analysis{}, err
		}
	}
	return a.Analyzer.Analyze(ctx, snapshot)
}

// handleStats reads the run record back.
//
// The numbers it reports were the ones I told the reader to watch and then left
// in a terminal log, where they survived until the next restart. This is the
// answer to "is the cap still cutting things that matter", "which feed has
// quietly died", and "what is this costing me".
func (a *App) handleStats(ctx context.Context, msg telegram.Message) error {
	if a.Runs == nil {
		return a.Bot.SendMessage(ctx, msg.Chat.ID, "No run record on this instance.")
	}
	return a.Bot.SendMessage(ctx, msg.Chat.ID, a.Runs.Summary(a.Cfg.DisplayLocation))
}

// quoteFor reads one share price, or nothing. A company that files with the SEC
// but trades elsewhere has no US quote, which the analysis says rather than
// guessing at.
func (a *App) quoteFor(ctx context.Context, ticker string) *model.Quote {
	if !a.Quotes.Enabled() {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	quotes, _ := a.Quotes.Fetch(ctx, []string{ticker})
	if len(quotes) == 0 {
		return nil
	}
	return &quotes[0]
}
