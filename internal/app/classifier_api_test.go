package app_test

import (
	"net/http"
	"testing"
)

func (f *fh) classifier(c *http.Client) map[string]any {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/classifier")
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("GET /api/classifier: status %d: %s", resp.StatusCode, body)
	}
	return decodeObject(f.t, body)
}

func TestTheClassifierRunsClaudeHaikuUnlessConfigured(t *testing.T) {
	f := startFh(t, t.TempDir())
	got := f.classifier(f.authorisedClient())
	if got["provider"] != "claude -p claude-haiku-5-5" || got["calls"] != float64(0) {
		t.Errorf("classifier = %v, want the default provider and no calls yet", got)
	}
}

func TestTheClassifierProviderIsSetInConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "[classifier]\nname = \"ollama llama3\"\ncommand = [\"ollama-classify\"]\n")
	f := startFhWith(t, appOptions(dir))
	if got := f.classifier(f.authorisedClient())["provider"]; got != "ollama llama3" {
		t.Errorf("provider = %v, want the configured one", got)
	}
}
