package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/joseph1009/market-watch/internal/relay"
)

// The daily run's closer look waits an hour after the brief. It is written to
// the data volume while it waits, so a restart or a deploy in that hour
// delays it rather than losing it, and RunLooks sends it when it falls due.

const (
	// LookDelay is how long after the daily brief its closer look is sent.
	// The brief is read first and on its own; the verdicts follow once it has
	// been, and the plan's allowance is not asked for both at once.
	LookDelay = time.Hour

	// lookPoll is how often a waiting look is checked for. A look is due on
	// the hour, so a minute late is on time.
	lookPoll = 30 * time.Second

	// lookStale is how late a look may be and still be sent: a process down
	// for most of a day should not greet its return with yesterday's verdicts.
	lookStale = 6 * time.Hour
)

func (a *App) lookPath() string { return filepath.Join(a.Cfg.DataDir, "pending-look.json") }

// queueLook writes a look to wait for its hour.
func (a *App) queueLook(lk look) error {
	data, err := json.Marshal(lk)
	if err != nil {
		return err
	}
	tmp := a.lookPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.lookPath())
}

// pendingLook is the look waiting, or nil.
func (a *App) pendingLook() (*look, error) {
	data, err := os.ReadFile(a.lookPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lk look
	if err := json.Unmarshal(data, &lk); err != nil {
		// A file that cannot be read never will be: drop it rather than try
		// every thirty seconds forever.
		_ = os.Remove(a.lookPath())
		return nil, err
	}
	return &lk, nil
}

// RunLooks sends each waiting closer look when it falls due, until the context
// ends.
func (a *App) RunLooks(ctx context.Context) error {
	for {
		wait := a.sendDueLook(ctx)
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// sendDueLook sends the waiting look if it is due, and says how long to wait
// before looking again.
func (a *App) sendDueLook(ctx context.Context) time.Duration {
	lk, err := a.pendingLook()
	if err != nil {
		a.Log.Warn("could not read the waiting closer look", "error", err)
	}
	if lk == nil {
		return lookPoll
	}
	late := a.now().Sub(lk.Due)
	switch {
	case late > lookStale:
		a.Log.Warn("dropped a closer look that waited too long", "due", lk.Due)
		_ = os.Remove(a.lookPath())
		return lookPoll
	case late >= 0:
		// Removed before it runs: a look that crashes the process halfway
		// must not run again, and post again, on restart. One that cannot be
		// removed is not run at all, since it would be sent every time round.
		if err := os.Remove(a.lookPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			a.Log.Warn("could not take the closer look off the queue; not sending it", "error", err)
			return lookPoll
		}
		a.runLook(ctx, *lk)
		return 0
	}
	return min(lookPoll, -late)
}

// runLook sends one closer look, as a run of its own.
func (a *App) runLook(ctx context.Context, lk look) {
	a.running.Lock()
	defer a.running.Unlock()

	if a.Relay != nil {
		var run *relay.Run
		var err error
		if ctx, run, err = a.Relay.Begin(ctx, "look"); err != nil {
			a.Log.Warn("could not start the closer look's run", "error", err)
			return
		}
		a.Log.Info("relay run", "dir", run.Dir)
	}
	a.sendIdeas(ctx, lk)
}
