package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// askScript makes the fake agent request permission on any prompt, then
// say which option it got ("permission=<option id>").
func askScript(extra map[string]any) script {
	req := map[string]any{"id": "t1", "title": "rm -rf build", "kind": "execute", "command": "rm -rf build", "path": "build/", "text": "removes the build folder"}
	for k, v := range extra {
		req[k] = v
	}
	return script{"default": []any{map[string]any{"permission": req}}}
}

// waitInbox reads events until the Inbox has n requests and returns them.
func waitInbox(t *testing.T, tab *websocket.Conn, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			break
		}
		if ev["type"] == "inbox" {
			if list := objects(t, ev, "inbox"); len(list) == n {
				return list
			}
		}
	}
	t.Fatalf("Inbox never had %d requests", n)
	return nil
}

func (f *fh) answer(c *http.Client, requestID, choice string) (int, map[string]any) {
	f.t.Helper()
	return f.post(c, "/api/inbox/"+requestID+"/answer", map[string]string{"choice": choice})
}

// askAndWait starts a Session whose starting prompt makes the Agent ask, and
// returns the tab, the Session ID and the open request.
func askAndWait(t *testing.T, f *fh) (*http.Client, *websocket.Conn, string, map[string]any) {
	t.Helper()
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "clean up"})
	req := waitInbox(t, tab, 1)[0]
	return c, tab, id, req
}

func lastAgentText(t *testing.T, f *fh, c *http.Client, id string) string {
	t.Helper()
	text := ""
	for _, it := range f.transcript(c, id) {
		if it["type"] == "agent" {
			text = it["text"].(string)
		}
	}
	return text
}

func permissionItem(t *testing.T, f *fh, c *http.Client, id string) map[string]any {
	t.Helper()
	for _, it := range f.transcript(c, id) {
		if it["type"] == "permission" {
			return it["permission"].(map[string]any)
		}
	}
	t.Fatalf("no permission card in Session %s", id)
	return nil
}

func TestAPermissionRequestWaitsInTheInboxAndTheSession(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c, _, id, req := askAndWait(t, f)

	if req["session"] != id || req["title"] != "rm -rf build" || req["kind"] != "execute" ||
		req["command"] != "rm -rf build" || req["content"] != "removes the build folder" {
		t.Errorf("Inbox request = %v", req)
	}
	if paths, _ := req["paths"].([]any); len(paths) != 1 || paths[0] != "build/" {
		t.Errorf("paths = %v, want [build/]", req["paths"])
	}
	card := permissionItem(t, f, c, id)
	if card["id"] != req["id"] || card["status"] != "pending" || card["always"] != false {
		t.Errorf("card = %v, want the same pending request without Always allow", card)
	}
	if s := f.sessionDetail(c, id); s["state"] != "waiting" {
		t.Errorf("state = %v, want waiting", s["state"])
	}
	if inbox := objects(t, f.state(c), "inbox"); len(inbox) != 1 {
		t.Errorf("snapshot inbox = %v, want the request", inbox)
	}
}

func TestAnsweringFromTheInboxReachesTheAgentAndClearsBothPlaces(t *testing.T) {
	for choice, want := range map[string]string{"allow": "allow-once", "deny": "reject", "always": "allow-always"} {
		t.Run(choice, func(t *testing.T) {
			f := startFhWithAgent(t, askScript(map[string]any{"always": true}))
			c, tab, id, req := askAndWait(t, f)

			if status, body := f.answer(c, req["id"].(string), choice); status != http.StatusNoContent {
				t.Fatalf("answer: status %d (%v), want 204", status, body)
			}
			waitInbox(t, tab, 0)
			waitState(t, tab, id, "idle")

			if got := lastAgentText(t, f, c, id); got != "permission="+want {
				t.Errorf("agent saw %q, want permission=%s", got, want)
			}
			wantStatus := map[string]string{"allow": "allowed", "deny": "denied", "always": "always"}[choice]
			if card := permissionItem(t, f, c, id); card["status"] != wantStatus {
				t.Errorf("card status = %v, want %s", card["status"], wantStatus)
			}
		})
	}
}

func TestDenyPicksRejectOnceEvenWhenRejectAlwaysComesFirst(t *testing.T) {
	s := askScript(nil)
	s["rejectAlwaysFirst"] = true
	f := startFhWithAgent(t, s)
	c, tab, id, req := askAndWait(t, f)

	f.answer(c, req["id"].(string), "deny")
	waitState(t, tab, id, "idle")

	if got := lastAgentText(t, f, c, id); got != "permission=reject" {
		t.Errorf("agent saw %q, want the reject_once option", got)
	}
}

func TestAlwaysAllowIsRefusedWhenTheAgentDidNotOfferIt(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c, _, _, req := askAndWait(t, f)

	if status, _ := f.answer(c, req["id"].(string), "always"); status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", status)
	}
}

func TestOnlyTheFirstAnswerCounts(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c, _, _, req := askAndWait(t, f)
	f.answer(c, req["id"].(string), "allow")

	status, body := f.answer(c, req["id"].(string), "deny")

	if status != http.StatusConflict || !strings.Contains(body["error"].(string), "already answered") {
		t.Fatalf("second answer: status %d (%v), want 409 already answered", status, body)
	}
}

func TestCancelWhileWaitingAnswersCancelledAndEndsTheTurn(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c, tab, id, _ := askAndWait(t, f)

	if status, _ := f.post(c, "/api/sessions/"+id+"/cancel", nil); status != http.StatusAccepted {
		t.Fatalf("cancel: status %d, want 202", status)
	}
	waitInbox(t, tab, 0)
	waitState(t, tab, id, "idle")

	if card := permissionItem(t, f, c, id); card["status"] != "cancelled" {
		t.Errorf("card status = %v, want cancelled", card["status"])
	}
	if got := lastAgentText(t, f, c, id); got != "permission=cancelled" {
		t.Errorf("agent saw %q, want the cancelled outcome", got)
	}
}

func TestARequestWhoseAgentExitedLeavesTheInbox(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{
		map[string]any{"exitAfter": 300},
		map[string]any{"permission": map[string]any{"id": "t1", "title": "rm x"}},
	}})
	c, tab, id, _ := askAndWait(t, f)

	waitState(t, tab, id, "exited")

	if inbox := objects(t, f.state(c), "inbox"); len(inbox) != 0 {
		t.Errorf("inbox = %v, want empty", inbox)
	}
	if card := permissionItem(t, f, c, id); card["status"] != "exited" {
		t.Errorf("card status = %v, want exited", card["status"])
	}
}

func TestTheInboxListsRequestsOldestFirst(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c := f.authorisedClient()
	tab := f.connectTab(c)
	var ids []string
	for _, name := range []string{"first", "second"} {
		repo := realPath(t, newRepo(t, t.TempDir(), name))
		f.addProject(c, repo)
		ids = append(ids, f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "go"}))
		waitInbox(t, tab, len(ids))
	}

	inbox := objects(t, f.state(c), "inbox")

	if len(inbox) != 2 || inbox[0]["session"] != ids[0] || inbox[1]["session"] != ids[1] {
		t.Errorf("inbox order = %v, want Session %s then %s", inbox, ids[0], ids[1])
	}
	if inbox[0]["sessionTitle"] != "main" || !strings.HasSuffix(inbox[0]["project"].(string), "first") {
		t.Errorf("inbox entry = %v, want the Session's title and Project", inbox[0])
	}
}

func TestAutoApproveStartsTheAgentInAutoMode(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"sayMode": true}}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)

	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "mode?", "autoApprove": true})
	s := waitTurn(t, tab, id)

	if s["autoApprove"] != true {
		t.Errorf("summary autoApprove = %v, want true", s["autoApprove"])
	}
	if got := lastAgentText(t, f, c, id); got != "mode=auto" {
		t.Errorf("agent saw %q, want mode=auto", got)
	}
}
