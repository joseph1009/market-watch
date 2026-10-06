package sprites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// api stands in for the Sprites API, answering the path given with the reply
// given, and recording what was asked.
func api(t *testing.T, replies map[string]func(w http.ResponseWriter)) (*Client, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		asked = append(asked, r.Method+" "+r.URL.Path)
		reply, ok := replies[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(w)
	}))
	t.Cleanup(srv.Close)
	return &Client{Token: "secret", Sprite: "dev", Service: "remote-control", BaseURL: srv.URL, HTTP: srv.Client()}, &asked
}

func write(body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { _, _ = w.Write([]byte(body)) }
}

const service = "/v1/sprites/dev/services/remote-control"

// The events the API sent when Remote Control was started on the Sprite, on
// 6 October 2026.
const started = `{"type":"started","timestamp":1}
{"type":"stdout","data":"remote-control: started, stopping after 120 quiet minutes\n","timestamp":2}
{"type":"stdout","data":"·|· Connecting · market-watch · main\n","timestamp":3}
{"type":"stdout","data":"\u001b[1A\u001b[J·✔︎· Ready · market-watch · main\n","timestamp":4}
{"type":"complete","log_files":{},"timestamp":5}
`

func TestStartWaitsForRemoteControlToBeReady(t *testing.T) {
	c, asked := api(t, map[string]func(http.ResponseWriter){"POST " + service + "/start": write(started)})
	ready, err := c.Start(context.Background(), "Ready")
	if err != nil || !ready {
		t.Fatalf("Start = %v, %v; want ready", ready, err)
	}
	if want := "POST " + service + "/start"; len(*asked) != 1 || (*asked)[0] != want {
		t.Errorf("asked %v, want %s", *asked, want)
	}
}

// A service already running is left alone: the API answers started and
// complete, with no output, and that is not ready.
func TestStartOfARunningServiceSaysNothing(t *testing.T) {
	c, _ := api(t, map[string]func(http.ResponseWriter){
		"POST " + service + "/start": write(`{"type":"started"}` + "\n" + `{"type":"complete"}` + "\n"),
	})
	ready, err := c.Start(context.Background(), "Ready")
	if err != nil || ready {
		t.Errorf("Start = %v, %v; want not ready, no error", ready, err)
	}
}

func TestStartReportsAServiceThatExits(t *testing.T) {
	c, _ := api(t, map[string]func(http.ResponseWriter){
		"POST " + service + "/start": write(`{"type":"started"}` + "\n" + `{"type":"exit","exit_code":1}` + "\n"),
	})
	if _, err := c.Start(context.Background(), "Ready"); err == nil || !strings.Contains(err.Error(), "status 1") {
		t.Errorf("Start error = %v, want the exit status", err)
	}
}

func TestStateReadsWhetherItRuns(t *testing.T) {
	c, _ := api(t, map[string]func(http.ResponseWriter){
		"GET " + service: write(`{"name":"remote-control","state":{"status":"running","started_at":"2026-10-06T14:18:15.226Z"}}`),
	})
	s, err := c.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Running || !s.Since.Equal(time.Date(2026, 10, 6, 14, 18, 15, 226e6, time.UTC)) {
		t.Errorf("State = %+v, want running since 14:18:15", s)
	}
}

// Stopping a stopped service is answered 409 "service is not running", which
// is not a failure.
func TestStopOfAStoppedServiceIsNotAnError(t *testing.T) {
	c, _ := api(t, map[string]func(http.ResponseWriter){
		"POST " + service + "/stop": func(w http.ResponseWriter) { http.Error(w, "service is not running", http.StatusConflict) },
	})
	was, err := c.Stop(context.Background())
	if err != nil || was {
		t.Errorf("Stop = %v, %v; want not running, no error", was, err)
	}
}

func TestStopOfARunningService(t *testing.T) {
	c, _ := api(t, map[string]func(http.ResponseWriter){
		"POST " + service + "/stop": write(`{"type":"stopping"}` + "\n" + `{"type":"stopped","exit_code":0}` + "\n"),
	})
	was, err := c.Stop(context.Background())
	if err != nil || !was {
		t.Errorf("Stop = %v, %v; want it was running", was, err)
	}
}

func TestAWrongTokenIsAnError(t *testing.T) {
	c, _ := api(t, nil)
	c.Token = "wrong"
	if _, err := c.State(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("State error = %v, want 401", err)
	}
}

func TestNoTokenIsDisabled(t *testing.T) {
	var c *Client
	if c.Enabled() || (&Client{}).Enabled() {
		t.Error("a client without a token should be disabled")
	}
}
