package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joseph1009/market-watch/internal/industry"
	"github.com/joseph1009/market-watch/internal/model"
	"github.com/joseph1009/market-watch/internal/relay"
	"github.com/joseph1009/market-watch/internal/telegram"
)

// heldModel answers only once released, and says when it has been asked.
type heldModel struct {
	asked   chan<- string
	release <-chan struct{}
	reply   string
}

func (m heldModel) Complete(ctx context.Context, _, prompt string) (string, model.Usage, error) {
	m.asked <- prompt
	select {
	case <-m.release:
	case <-ctx.Done():
		return "", model.Usage{}, ctx.Err()
	}
	return m.reply, model.Usage{}, nil
}

// waitFor takes n values off ch, failing the test if they are slow to come.
func waitFor[T any](t *testing.T, ch <-chan T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d arrived", i, n)
		}
	}
}

// count is how many of the texts contain want.
func count(texts []string, want string) int {
	n := 0
	for _, s := range texts {
		if strings.Contains(s, want) {
			n++
		}
	}
	return n
}

// A second /industry starts while the first is still being written, rather
// than waiting minutes behind it, and both are answered.
func TestTwoIndustriesAreWrittenAtTheSameTime(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	asked, release := make(chan string, 2), make(chan struct{})
	a.Industry = &industry.Explainer{
		Completer: heldModel{asked: asked, release: release, reply: "### 🧭 The big picture\n- Parts and makers.\n\nCOMPANIES BY PART\n"},
		Verifier:  knownTickers{},
	}

	a.HandleMessage(context.Background(), message("/industry robotics"))
	a.HandleMessage(context.Background(), message("/industry batteries"))
	waitFor(t, asked, 2) // both are with the model at once
	close(release)
	a.work.wait()

	owner := strings.Join(messagesTo(*sent, 4242), "\n")
	for _, want := range []string{"Robotics — how the industry fits together", "Batteries — how the industry fits together"} {
		if !strings.Contains(owner, want) {
			t.Errorf("owner was not sent %q:\n%s", want, owner)
		}
	}
}

// heldJob is a job that says when it starts, waits to be released, and then
// reports the plan's standing as a model call would.
func heldJob(a *App, name string, started chan<- string, release <-chan struct{}, used float64) func(context.Context) error {
	return func(context.Context) error {
		started <- name
		<-release
		a.plan.note(relay.Limits{Status: "allowed", Windows: map[string]relay.Window{
			"five_hour": {Utilization: 0.30}, "seven_day": {Utilization: used},
		}})
		return nil
	}
}

// planAt notes a reading with the week used.
func planAt(a *App, used float64) {
	a.plan = &planWatch{}
	a.plan.note(relay.Limits{Status: "allowed", Windows: map[string]relay.Window{
		"five_hour": {Utilization: 0.30}, "seven_day": {Utilization: used},
	}})
}

// nothingStarts fails the test if a job starts within a moment.
func nothingStarts(t *testing.T, started <-chan string, why string) {
	t.Helper()
	select {
	case name := <-started:
		t.Fatalf("%s started %s", why, name)
	case <-time.After(200 * time.Millisecond):
	}
}

// Past 85% of any window, the chat is warned once and the jobs run one at a
// time, in the order they were asked for, each that waits saying so. When
// all are done, the chat is told how much of the plan is used, once.
func TestPastEightyFivePercentJobsRunOneAtATimeInOrder(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	planAt(a, 0.87)

	started, release := make(chan string, 3), make(chan struct{})
	for _, ticker := range []string{"MU", "NVDA", "AMD"} {
		a.background(context.Background(), message("/analyse "+ticker), "analyse", heldJob(a, ticker, started, release, 0.88))
	}
	if first := <-started; first != "MU" {
		t.Errorf("%s started first, want MU, the first asked for", first)
	}
	nothingStarts(t, started, "past 85%, a second job")
	close(release)
	for _, want := range []string{"NVDA", "AMD"} {
		select {
		case got := <-started:
			if got != want {
				t.Errorf("%s started, want %s next", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never started", want)
		}
	}
	a.work.wait()

	got := messagesTo(*sent, 4242)
	if n := count(got, "⚠️ <b>The Claude plan is 87% used</b> (this week). From now on I run one request at a time"); n != 1 {
		t.Errorf("warned %d times, want once: %q", n, got)
	}
	if n := count(got, "⏳ The Claude plan is over 85% used, so I run one request at a time."); n != 2 {
		t.Errorf("told %d jobs to wait, want two: %q", n, got)
	}
	if n := count(got, "📊 <b>Claude plan used</b>\nNext five hours: <b>30%</b>\nThis week: <b>88%</b>\n⚠️ Over 85%"); n != 1 {
		t.Errorf("sent the usage %d times, want once, after the jobs: %q", n, got)
	}
	if last := got[len(got)-1]; !strings.HasPrefix(last, "📊") {
		t.Errorf("the usage was not the last word: %q", last)
	}
}

// Below 85%, three jobs run at once with no warning, and a fourth waits for
// one of them, saying so.
func TestBelowEightyFivePercentThreeJobsRunAtOnce(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	planAt(a, 0.40)

	started, release := make(chan string, 4), make(chan struct{})
	for _, ticker := range []string{"MU", "NVDA", "AMD", "TSM"} {
		a.background(context.Background(), message("/analyse "+ticker), "analyse", heldJob(a, ticker, started, release, 0.41))
	}
	waitFor(t, started, 3)
	nothingStarts(t, started, "a fourth job")
	close(release)
	waitFor(t, started, 1)
	a.work.wait()

	got := messagesTo(*sent, 4242)
	if count(got, "⚠️") != 0 {
		t.Errorf("warned below 85%%: %q", got)
	}
	if n := count(got, "⏳ Three requests are already running."); n != 1 {
		t.Errorf("told %d jobs to wait, want one: %q", n, got)
	}
	if n := count(got, "This week: <b>41%</b>"); n != 1 {
		t.Errorf("sent the usage %d times, want once: %q", n, got)
	}
}

// While a brief is being written no job starts, and the chat is told why.
// They start once it is done.
func TestJobsWaitForABrief(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	planAt(a, 0.40)
	if err := a.work.hold(context.Background()); err != nil {
		t.Fatal(err)
	}

	started, release := make(chan string, 2), make(chan struct{})
	close(release)
	a.background(context.Background(), message("/analyse MU"), "analyse", heldJob(a, "MU", started, release, 0.41))
	a.background(context.Background(), message("/industry robotics"), "industry", heldJob(a, "robotics", started, release, 0.41))
	nothingStarts(t, started, "during a brief, a job")
	a.work.release()
	waitFor(t, started, 2)
	a.work.wait()

	if n := count(messagesTo(*sent, 4242), "⏳ A brief is being written."); n != 2 {
		t.Errorf("told %d jobs why they wait, want two: %q", n, messagesTo(*sent, 4242))
	}
}

// A brief waits for the jobs already running to finish before it starts, so
// it never runs beside them.
func TestABriefWaitsForTheJobsRunning(t *testing.T) {
	a, _ := newTestApp(t)
	a.prefs.ChatID = 4242
	planAt(a, 0.40)

	started, release := make(chan string, 1), make(chan struct{})
	a.background(context.Background(), message("/analyse MU"), "analyse", heldJob(a, "MU", started, release, 0.41))
	waitFor(t, started, 1)

	held := make(chan error, 1)
	go func() { held <- a.work.hold(context.Background()) }()
	select {
	case <-held:
		t.Fatal("the brief started beside a running job")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-held:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the brief never started")
	}
	a.work.release()
	a.work.wait()
}

// A job that never reached a model, such as one whose ticker could not be
// read, spent nothing, so it is not followed by the plan's standing.
func TestAJobThatAskedNoModelSendsNoUsage(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242
	a.plan = &planWatch{}
	a.plan.note(relay.Limits{Status: "allowed", Windows: map[string]relay.Window{"seven_day": {Utilization: 0.40}}})
	time.Sleep(10 * time.Millisecond) // the reading is older than the job

	a.background(context.Background(), message("/analyse ZZZZ"), "analyse", func(ctx context.Context) error {
		return a.Bot.SendMessage(ctx, 4242, "I could not read ZZZZ.")
	})
	a.work.wait()

	if got := messagesTo(*sent, 4242); count(got, "📊") != 0 {
		t.Errorf("sent the usage after a job that asked no model: %q", got)
	}
}

// A job that fails says so in the chat, as a command handled in turn does.
func TestAFailedJobSaysSo(t *testing.T) {
	a, sent := newTestApp(t)
	a.prefs.ChatID = 4242

	a.background(context.Background(), telegram.Message{Text: "/analyse MU", Chat: telegram.Chat{ID: 4242}}, "analyse",
		func(context.Context) error { return context.DeadlineExceeded })
	a.work.wait()

	if got := messagesTo(*sent, 4242); count(got, "Something went wrong: context deadline exceeded") != 1 {
		t.Errorf("replies = %q", got)
	}
}
