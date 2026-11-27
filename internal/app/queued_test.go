package app_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/coder/websocket"
)

// Queued message (#93): a message sent while the Session is Starting,
// Working or Waiting waits on the server and goes out as its own turn once
// the current turn ends.

// startWorking starts a Session whose starting prompt "long" runs until
// cancelled, and waits until it is Working. tab connected before the
// Session started, so it sees every change after that (#151).
func startWorking(t *testing.T, s script) (f *fh, c *http.Client, id string, tab *websocket.Conn) {
	t.Helper()
	f = startFhWithAgent(t, s)
	c, tab, id, _ = newSessionIn(t, f, "long")
	waitState(t, tab, id, "working")
	return f, c, id, tab
}

func userTexts(items []map[string]any) []string {
	var out []string
	for _, it := range items {
		if it["type"] == "user" {
			out = append(out, it["text"].(string))
		}
	}
	return out
}

func TestAMessageSentWhileWorkingIsSentAsTheNextTurnWithoutAWaitingStop(t *testing.T) {
	t.Parallel()
	// The first turn holds until the message is queued, however slow the
	// machine (#220).
	gate, openGate := turnGate(t)
	f := startFhWithAgent(t, script{"prompts": map[string]any{
		"long": []any{map[string]any{"waitFile": gate}, map[string]any{"say": "first"}},
		"more": []any{map[string]any{"say": "second"}},
	}})
	c, tab, id, _ := newSessionIn(t, f, "long")
	waitState(t, tab, id, "working")

	if status, body := f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "more"}); status != http.StatusAccepted {
		t.Fatalf("prompt while working: status %d (%v), want 202", status, body)
	}
	if q := f.sessionDetail(c, id)["queued"]; q != "more" {
		t.Errorf("queued = %v, want %q", q, "more")
	}
	openGate()

	// Between the two turns the Session never stops at "finished a turn".
	var states []string
	s := waitFor(t, tab, id, "the queued turn ended", func(s map[string]any) bool {
		states = append(states, s["state"].(string)+"/"+asString(s["waiting"]))
		return s["state"] == "waiting" && s["waiting"] == "turn"
	})
	if n := countOf(states, "waiting/turn"); n != 1 {
		t.Errorf("states %v: want exactly one waiting/turn, at the very end", states)
	}
	if s["queued"] != nil {
		t.Errorf("queued = %v after it was sent, want none", s["queued"])
	}
	if got := userTexts(f.transcript(c, id)); !slices.Equal(got, []string{"long", "more"}) {
		t.Errorf("user turns %v, want [long more]", got)
	}
}

func TestSendingAgainAddsToTheQueuedMessage(t *testing.T) {
	t.Parallel()
	f, c, id, _ := startWorking(t, script{"prompts": map[string]any{"long": []any{map[string]any{"waitCancel": true}}}})

	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "one"})
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "two"})

	if q := f.sessionDetail(c, id)["queued"]; q != "one\n\ntwo" {
		t.Errorf("queued = %q, want %q", q, "one\n\ntwo")
	}
}

func TestCancelMovesTheQueuedMessageBackInsteadOfSendingIt(t *testing.T) {
	t.Parallel()
	f, c, id, _ := startWorking(t, script{"prompts": map[string]any{"long": []any{map[string]any{"waitCancel": true}}}})
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "more"})

	f.post(c, "/api/sessions/"+id+"/cancel", nil)
	s := waitState(t, tab, id, "idle")

	if s["queued"] != nil || s["unsent"] != "more" {
		t.Errorf("queued %v, unsent %v; want none queued and %q back for the composer", s["queued"], s["unsent"], "more")
	}
	if got := userTexts(f.transcript(c, id)); !slices.Equal(got, []string{"long"}) {
		t.Errorf("user turns %v, want only [long]", got)
	}
}

func TestEditTakesTheQueuedMessageBackAndRemoveDropsIt(t *testing.T) {
	t.Parallel()
	f, c, id, _ := startWorking(t, script{"prompts": map[string]any{"long": []any{map[string]any{"waitCancel": true}}}})

	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "more"})
	status, body := f.post(c, "/api/sessions/"+id+"/queued", map[string]string{"action": "edit"})
	if status != http.StatusOK || body["text"] != "more" {
		t.Fatalf("edit: status %d (%v), want 200 with text %q", status, body, "more")
	}
	if q := f.sessionDetail(c, id)["queued"]; q != nil {
		t.Errorf("queued = %v after edit, want none", q)
	}

	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "other"})
	if status, _ := f.post(c, "/api/sessions/"+id+"/queued", map[string]string{"action": "remove"}); status != http.StatusNoContent {
		t.Fatalf("remove: status %d, want 204", status)
	}
	if q := f.sessionDetail(c, id)["queued"]; q != nil {
		t.Errorf("queued = %v after remove, want none", q)
	}
}

func TestSendNowCancelsTheTurnAndSendsTheQueuedMessage(t *testing.T) {
	t.Parallel()
	f, c, id, _ := startWorking(t, script{"prompts": map[string]any{
		"long": []any{map[string]any{"waitCancel": true}},
		"more": []any{map[string]any{"say": "done"}},
	}})
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "more"})

	if status, body := f.post(c, "/api/sessions/"+id+"/queued", map[string]string{"action": "send-now"}); status != http.StatusAccepted {
		t.Fatalf("send-now: status %d (%v), want 202", status, body)
	}
	s := waitEnded(t, tab, id)

	if s["queued"] != nil || s["unsent"] != nil {
		t.Errorf("queued %v, unsent %v; want both empty", s["queued"], s["unsent"])
	}
	if got := userTexts(f.transcript(c, id)); !slices.Equal(got, []string{"long", "more"}) {
		t.Errorf("user turns %v, want [long more]", got)
	}
}

func TestAQueuedMessageIsNeverLostWhenTheAgentExits(t *testing.T) {
	t.Parallel()
	// The Agent exits once "more" is queued: the turn holds on a gate until
	// then, however slow the machine (#220). The tab connected before the
	// Session started, so it sees the exit.
	gate, openGate := turnGate(t)
	f, c, id, tab := startWorking(t, script{"prompts": map[string]any{
		"long": []any{map[string]any{"waitFile": gate}, map[string]any{"exit": 3}},
	}})
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "more"})
	openGate()

	s := waitState(t, tab, id, "exited")

	if s["queued"] != nil || s["unsent"] != "more" {
		t.Errorf("queued %v, unsent %v; want %q back for the composer", s["queued"], s["unsent"], "more")
	}
}

func TestAMessageSentWhileStartingIsSentOnceTheAgentIsReady(t *testing.T) {
	t.Parallel()
	// The Agent's start holds until the message is sent, so the Session is
	// still Starting when it arrives, however slow the machine (#220).
	gate, openGate := turnGate(t)
	f := startFhWithAgent(t, script{
		"onStart": []any{map[string]any{"waitFile": gate}},
		"prompts": map[string]any{"early": []any{map[string]any{"say": "hi"}}},
	})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude"})

	if status, body := f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "early"}); status != http.StatusAccepted {
		t.Fatalf("prompt while starting: status %d (%v), want 202", status, body)
	}
	openGate()
	waitEnded(t, tab, id)

	if got := userTexts(f.transcript(c, id)); !slices.Equal(got, []string{"early"}) {
		t.Errorf("user turns %v, want [early]", got)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func countOf(xs []string, x string) int {
	n := 0
	for _, v := range xs {
		if v == x {
			n++
		}
	}
	return n
}

func TestTheComposerClearsUnsentOnceItTookIt(t *testing.T) {
	t.Parallel()
	f, c, id, _ := startWorking(t, script{"prompts": map[string]any{"long": []any{map[string]any{"waitCancel": true}}}})
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "more"})
	f.post(c, "/api/sessions/"+id+"/cancel", nil)
	waitState(t, tab, id, "idle")

	if status, _ := f.post(c, "/api/sessions/"+id+"/queued", map[string]string{"action": "clear-unsent"}); status != http.StatusNoContent {
		t.Fatalf("clear-unsent: status %d, want 204", status)
	}
	if u := f.sessionDetail(c, id)["unsent"]; u != nil {
		t.Errorf("unsent = %v, want none", u)
	}
}

func TestSendNowNeedsARunningTurn(t *testing.T) {
	t.Parallel()
	// The Agent's start holds until the test is done, so the Session is
	// still Starting at send-now, however slow the machine (#220).
	gate, openGate := turnGate(t)
	f := startFhWithAgent(t, script{"onStart": []any{map[string]any{"waitFile": gate}}})
	t.Cleanup(openGate)
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude"})
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "early"})

	// While Starting there is no turn to cancel; the message goes out once
	// the Agent is ready anyway.
	if status, body := f.post(c, "/api/sessions/"+id+"/queued", map[string]string{"action": "send-now"}); status != http.StatusConflict {
		t.Fatalf("send-now while starting: status %d (%v), want 409", status, body)
	}
}

func TestAnUnknownQueuedActionIsABadRequest(t *testing.T) {
	t.Parallel()
	f, c, id, _ := startWorking(t, script{"prompts": map[string]any{"long": []any{map[string]any{"waitCancel": true}}}})
	if status, _ := f.post(c, "/api/sessions/"+id+"/queued", map[string]string{"action": "shout"}); status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", status)
	}
}

func TestUnsentStaysUntilTheComposerTookIt(t *testing.T) {
	t.Parallel()
	f, c, id, _ := startWorking(t, script{"prompts": map[string]any{
		"long": []any{map[string]any{"waitCancel": true}},
		"next": []any{map[string]any{"waitCancel": true}},
	}})
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "more"})
	f.post(c, "/api/sessions/"+id+"/cancel", nil)
	waitState(t, tab, id, "idle")

	// Another send doesn't drop what came back.
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "next"})
	if u := f.sessionDetail(c, id)["unsent"]; u != "more" {
		t.Errorf("unsent = %v, want %q kept", u, "more")
	}
}
