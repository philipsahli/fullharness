package app_test

import (
	"net/http"
	"testing"
)

// commandNames returns the names in a Session summary's "commands".
func commandNames(t *testing.T, s map[string]any) []string {
	t.Helper()
	raw, _ := s["commands"].([]any)
	var out []string
	for _, c := range raw {
		out = append(out, c.(map[string]any)["name"].(string))
	}
	return out
}

func TestASessionListsTheSlashCommandsOfItsLatestUpdate(t *testing.T) {
	f := startFhWithAgent(t, script{
		"onStart": []any{commands("grill-me", "mattpocock-skills:tdd")},
		"prompts": map[string]any{"install": []any{commands("grill-me", "mattpocock-skills:tdd", "retro")}},
	})
	c, _, id, _ := newSessionIn(t, f, "")

	s := f.sessionDetail(c, id)
	cmds := objects(t, s, "commands")
	if len(cmds) != 2 || cmds[0]["name"] != "grill-me" || cmds[0]["description"] != "fake grill-me" {
		t.Fatalf("commands = %v, want grill-me (fake grill-me) and mattpocock-skills:tdd", cmds)
	}

	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "install"})
	s = waitFor(t, tab, id, "three commands", func(s map[string]any) bool { return len(commandNames(t, s)) == 3 })
	if got := commandNames(t, s); got[2] != "retro" {
		t.Errorf("commands after the second update = %v, want retro last", got)
	}
	// The Agent's cache follows the latest list too (skill resolution, #13).
	if a := f.skill(c, "retro")["claude"]; a.Skill != "/retro" {
		t.Errorf("claude resolves retro as %+v, want /retro from the latest list", a)
	}
}

// option returns the config option id of a Session summary.
func option(t *testing.T, s map[string]any, id string) map[string]any {
	t.Helper()
	raw, _ := s["options"].([]any)
	for _, o := range raw {
		if o := o.(map[string]any); o["id"] == id {
			return o
		}
	}
	t.Fatalf("no option %s in %v", id, s["options"])
	return nil
}

func TestTheModelCanBeChangedThroughTheAgentsConfigOption(t *testing.T) {
	f := startFhWithAgent(t, script{"models": true, "default": []any{map[string]any{"sayConfig": "model"}}})
	c, _, id, _ := newSessionIn(t, f, "")

	model := option(t, f.sessionDetail(c, id), "model")
	if model["category"] != "model" || model["value"] != "sonnet" || len(model["values"].([]any)) != 2 {
		t.Fatalf("model option = %v, want category model, sonnet of two values", model)
	}

	if status, _ := f.post(c, "/api/sessions/"+id+"/option", map[string]string{"option": "model", "value": "gpt-9"}); status != http.StatusBadRequest {
		t.Errorf("a model the Agent doesn't offer: status %d, want 400", status)
	}
	tab := f.connectTab(c)
	if status, body := f.post(c, "/api/sessions/"+id+"/option", map[string]string{"option": "model", "value": "opus"}); status != http.StatusNoContent {
		t.Fatalf("set model opus: status %d (%v), want 204", status, body)
	}
	waitFor(t, tab, id, "model opus", func(s map[string]any) bool { return option(t, s, "model")["value"] == "opus" })
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "which model?"})
	waitTurn(t, tab, id)
	if got := f.lastAgentText(c, id); got != "model=opus" {
		t.Errorf("the Agent uses %v, want model=opus", got)
	}
}

func TestASessionShowsContextUseAndCostFromUsageUpdates(t *testing.T) {
	f := startFhWithAgent(t, script{"prompts": map[string]any{
		"copilot-like": []any{map[string]any{"usage": map[string]any{"used": 12000, "size": 200000}}},
		"claude-like":  []any{map[string]any{"usage": map[string]any{"used": 53000, "size": 200000, "cost": 0.42, "currency": "USD"}}},
	}})
	c, _, id, _ := newSessionIn(t, f, "")
	if u := f.sessionDetail(c, id)["usage"]; u != nil {
		t.Errorf("usage before any update = %v, want none", u)
	}
	tab := f.connectTab(c)

	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "copilot-like"})
	s := waitTurn(t, tab, id)
	u, _ := s["usage"].(map[string]any)
	if u["used"] != float64(12000) || u["size"] != float64(200000) || u["cost"] != nil {
		t.Errorf("usage = %v, want 12000 of 200000 and no cost: this Agent reports none", u)
	}

	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "claude-like"})
	s = waitTurn(t, tab, id)
	u, _ = s["usage"].(map[string]any)
	cost, _ := u["cost"].(map[string]any)
	if u["used"] != float64(53000) || cost["amount"] != 0.42 || cost["currency"] != "USD" {
		t.Errorf("usage = %v, want 53000 used and a cost of 0.42 USD", u)
	}
}

// lastAgentText is the text of the last item of Session id's conversation.
func (f *fh) lastAgentText(c *http.Client, id string) any {
	f.t.Helper()
	items := f.transcript(c, id)
	return items[len(items)-1]["text"]
}

func TestTheModeCanBeChangedAmongTheModesTheAgentOffers(t *testing.T) {
	f := startFhWithAgent(t, script{
		"prompts": map[string]any{"which": []any{map[string]any{"sayMode": true}}},
		"default": []any{map[string]any{"switchMode": "default"}},
	})
	c, _, id, _ := newSessionIn(t, f, "")

	// The fake agent starts in "auto"; fh switched it to asking first.
	s := f.sessionDetail(c, id)
	if s["mode"] != "default" || len(objects(t, s, "modes")) != 3 {
		t.Fatalf("mode %v of %v, want default of the fake agent's three modes", s["mode"], s["modes"])
	}

	if status, body := f.post(c, "/api/sessions/"+id+"/mode", map[string]string{"mode": "yolo"}); status != http.StatusBadRequest {
		t.Errorf("unknown mode: status %d (%v), want 400", status, body)
	}
	tab := f.connectTab(c)
	if status, body := f.post(c, "/api/sessions/"+id+"/mode", map[string]string{"mode": "plan"}); status != http.StatusNoContent {
		t.Fatalf("set mode plan: status %d (%v), want 204", status, body)
	}
	waitFor(t, tab, id, "mode plan", func(s map[string]any) bool { return s["mode"] == "plan" })
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "which"})
	waitTurn(t, tab, id)
	if got := f.lastAgentText(c, id); got != "mode=plan" {
		t.Errorf("the Agent is in %v, want mode=plan", got)
	}

	// An Agent may change its mode itself, e.g. Claude Code leaving plan mode.
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "build it"})
	waitFor(t, tab, id, "mode default", func(s map[string]any) bool { return s["mode"] == "default" })
}

func TestChoosingTheAllowWithoutAskingSettingTurnsAutoApproveOnAndOff(t *testing.T) {
	for _, tc := range []struct {
		agent, path, key, auto, ask string
	}{
		{"claude", "mode", "mode", "auto", "default"}, // Claude Code's mode
		{"copilot", "option", "value", "on", "off"},   // Copilot CLI's allow_all option
	} {
		t.Run(tc.agent, func(t *testing.T) {
			f := startFhWithAgents(t, map[string]script{tc.agent: {"allowAll": true}}, nil)
			c := f.authorisedClient()
			repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
			f.addProject(c, repo)
			tab := f.connectTab(c)
			id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": tc.agent})
			waitState(t, tab, id, "idle")
			choose := func(value string) {
				t.Helper()
				body := map[string]string{tc.key: value}
				if tc.path == "option" {
					body["option"] = "allow_all"
				}
				if status, resp := f.post(c, "/api/sessions/"+id+"/"+tc.path, body); status != http.StatusNoContent {
					t.Fatalf("choose %s: status %d (%v), want 204", value, status, resp)
				}
			}

			choose(tc.auto)
			waitFor(t, tab, id, "auto-approve on", func(s map[string]any) bool { return s["autoApprove"] == true })
			choose(tc.ask)
			waitFor(t, tab, id, "auto-approve off", func(s map[string]any) bool { return s["autoApprove"] == false })
		})
	}
}
