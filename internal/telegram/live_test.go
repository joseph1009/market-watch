package telegram

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// A bot token is easy to get wrong and the failure only shows up at delivery
// time, hours after collection. getMe proves the token and the network path
// without sending anything to a chat, so it is safe to run on demand.
//
//	MARKET_WATCH_LIVE=1 go test ./internal/telegram -run TestLive -v
func TestLiveBotTokenIsValid(t *testing.T) {
	if os.Getenv("MARKET_WATCH_LIVE") == "" {
		t.Skip("set MARKET_WATCH_LIVE=1 to check the bot token against Telegram")
	}
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		t.Skip("TELEGRAM_BOT_TOKEN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	username, err := New(token, &http.Client{Timeout: 20 * time.Second}).Me(ctx)
	if err != nil {
		t.Fatalf("getMe: %v", err)
	}
	t.Logf("authenticated as @%s", username)
}
