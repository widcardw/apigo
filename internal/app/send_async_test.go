package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestSendDoesNotBlockOtherRequests checks a request the server never answers
// holds only its own tab: the window keeps drawing, and another tab sends and
// shows its response meanwhile.
func TestSendDoesNotBlockOtherRequests(t *testing.T) {
	hang := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hang // never answers until the test is over
	}))
	defer slow.Close()
	// Registered after the server, so it runs before Close waits for the request
	// still in the handler.
	defer close(hang)
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer fast.Close()

	a := testApp(t)
	pending := make(chan func(), 4)
	a.after = func(run func()) { pending <- run }
	tt := ui.NewTester(a.view, 1280, 800)

	// The first tab asks a server that never answers.
	a.current().url = slow.URL
	if err := tt.Click("发送"); err != nil {
		t.Fatal(err)
	}
	if !a.current().loading {
		t.Fatal("the first request is not waiting")
	}

	// A second tab sends to a server that answers, and its response arrives
	// while the first is still waiting.
	a.addTab(a.space())
	a.current().url = fast.URL
	tt.Frame()
	if err := tt.Click("发送"); err != nil {
		t.Fatal(err)
	}
	waitForApply(t, pending)

	second := a.current()
	if second.loading || second.resp == nil {
		t.Fatalf("loading %v response %v, want the second response in", second.loading, second.resp)
	}
	if second.resp.Status != http.StatusOK {
		t.Errorf("status %d, want 200", second.resp.Status)
	}
	if !a.space().tabs[0].loading {
		t.Error("the first request stopped waiting before the server answered")
	}

	// The window still draws and takes input while the first one waits.
	tt.Frame()
	if err := tt.Click("标签页 Pets"); err != nil {
		t.Fatalf("the window stopped taking clicks: %v", err)
	}
	if got := a.space().active; got != 0 {
		t.Errorf("active tab %d after clicking the first one, want 0", got)
	}
}

func TestSendUsesEnvironmentHeadersAndRequestOverrides(t *testing.T) {
	var received http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
	}))
	defer server.Close()

	a := testApp(t)
	a.current().url = server.URL
	a.current().bodyType = "none"
	env := a.store.Environment("env-1")
	env.Vars = append(env.Vars, savedRow{Enabled: true, Key: "session", Value: "shared"})
	env.Headers = []savedRow{
		{Enabled: true, Key: "Cookie", Value: "sid={{session}}"},
		{Enabled: true, Key: "X-Default", Value: "environment"},
		{Enabled: true, Key: "X-Override", Value: "environment"},
		{Enabled: false, Key: "X-Disabled", Value: "ignored"},
	}
	a.current().headers.Rows = rowsFromSaved([]savedRow{
		{Enabled: true, Key: "X-Override", Value: "request"},
	})

	a.send(nil)

	if got := received.Get("Cookie"); got != "sid=shared" {
		t.Errorf("Cookie = %q, want the environment value with its variable resolved", got)
	}
	if got := received.Get("X-Default"); got != "environment" {
		t.Errorf("X-Default = %q, want the environment fallback", got)
	}
	if got := received.Get("X-Override"); got != "request" {
		t.Errorf("X-Override = %q, want the request-level value to take precedence", got)
	}
	if got := received.Get("X-Disabled"); got != "" {
		t.Errorf("X-Disabled = %q, want the disabled environment header omitted", got)
	}
}

// waitForApply runs the work the window would run for a finished response.
func waitForApply(t *testing.T, pending <-chan func()) {
	t.Helper()
	select {
	case run := <-pending:
		run()
	case <-time.After(5 * time.Second):
		t.Fatal("the response never reached the window")
	}
}
