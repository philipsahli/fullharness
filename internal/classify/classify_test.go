package classify_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/philipsahli/fullharness/internal/classify"
)

// fakeProvider answers every message with text, or fails with err.
type fakeProvider struct {
	name string
	text string
	err  error
	// answers, when set, maps a message to its answer.
	answers map[string]string
	calls   int
	last    string // the last message it read
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Classify(_ context.Context, message string) (classify.Answer, error) {
	p.calls++
	p.last = message
	if p.err != nil {
		return classify.Answer{}, p.err
	}
	text := p.text
	if a, ok := p.answers[message]; ok {
		text = a
	}
	return classify.Answer{Text: text, InputTokens: 120, OutputTokens: 1, CostUSD: 0.0001}, nil
}

func newClassifier(t *testing.T, p classify.Provider) (*classify.Classifier, string) {
	t.Helper()
	dir := t.TempDir()
	c, err := classify.New(p, dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func turn(message string) classify.Input {
	return classify.Input{Session: "s1", StopReason: "end_turn", Message: message}
}

func TestTheProviderClassifiesAnUnclearTurnEnd(t *testing.T) {
	for answer, want := range map[string]classify.Verdict{
		"asking":    classify.Asking,
		"done":      classify.Done,
		"stuck":     classify.Stuck,
		" Done.\n":  classify.Done,
		"ASKING":    classify.Asking,
		"maybe":     classify.Unsure,
		"done, but": classify.Unsure,
		"":          classify.Unsure,
	} {
		c, _ := newClassifier(t, &fakeProvider{name: "fake", text: answer})
		r := c.Classify(context.Background(), turn("I changed the form."))
		if r.Verdict != want {
			t.Errorf("answer %q: verdict %q, want %q", answer, r.Verdict, want)
		}
		if r.StopPoint() != (want != classify.Done) {
			t.Errorf("answer %q: StopPoint %v; only done goes on without the user", answer, r.StopPoint())
		}
	}
}

func TestAProviderErrorIsAStopPoint(t *testing.T) {
	c, _ := newClassifier(t, &fakeProvider{name: "fake", err: errors.New("claude: not logged in")})
	r := c.Classify(context.Background(), turn("I changed the form."))
	if r.Verdict != classify.Unsure || !r.StopPoint() {
		t.Errorf("verdict %q, StopPoint %v; want unsure and a Stop point", r.Verdict, r.StopPoint())
	}
	s, err := c.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s.Calls != 1 || s.Errors != 1 {
		t.Errorf("stats %+v, want 1 call that failed", s)
	}
}

func TestRulesDecideWithoutTheProvider(t *testing.T) {
	cases := []struct {
		name string
		in   classify.Input
		want classify.Verdict
	}{
		{"open permission request", classify.Input{StopReason: "end_turn", Message: "Done.", PendingPermission: true}, classify.Asking},
		{"the Agent refused the turn", classify.Input{Err: "rate limited"}, classify.Stuck},
		{"refusal", classify.Input{StopReason: "refusal", Message: "I can't help."}, classify.Stuck},
		{"max_tokens", classify.Input{StopReason: "max_tokens", Message: "and then"}, classify.Stuck},
		{"cancelled by the user", classify.Input{StopReason: "cancelled"}, classify.Asking},
		{"no message to read", classify.Input{StopReason: "end_turn", Message: "  "}, classify.Unsure},
	}
	for _, tc := range cases {
		p := &fakeProvider{name: "fake", text: "done"}
		c, _ := newClassifier(t, p)
		r := c.Classify(context.Background(), tc.in)
		if r.Verdict != tc.want {
			t.Errorf("%s: verdict %q, want %q", tc.name, r.Verdict, tc.want)
		}
		if !strings.HasPrefix(r.By, "rule:") {
			t.Errorf("%s: decided by %q, want a rule", tc.name, r.By)
		}
		if p.calls != 0 {
			t.Errorf("%s: the provider was called %d times, want 0", tc.name, p.calls)
		}
	}
}

func TestEachCallIsMeasured(t *testing.T) {
	c, _ := newClassifier(t, &fakeProvider{name: "fake", text: "done"})
	c.Classify(context.Background(), turn("one"))
	c.Classify(context.Background(), turn("two"))

	s, err := c.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s.Calls != 2 || s.InputTokens != 240 || s.OutputTokens != 2 {
		t.Errorf("stats %+v, want 2 calls, 240 input and 2 output tokens", s)
	}
	if s.CostUSD < 0.00019 || s.CostUSD > 0.00021 {
		t.Errorf("cost %v, want 0.0002", s.CostUSD)
	}
	if s.MeanLatencyMS <= 0 {
		t.Errorf("mean latency %vms, want it measured", s.MeanLatencyMS)
	}
}

func TestAnOverrideIsCountedAgainstTheVerdict(t *testing.T) {
	c, _ := newClassifier(t, &fakeProvider{name: "fake", text: "done"})
	wrong := c.Classify(context.Background(), turn("Should I open the PR?"))
	right := c.Classify(context.Background(), turn("All done."))

	if err := c.Override(wrong.ID, classify.Asking); err != nil {
		t.Fatal(err)
	}
	// Confirming the verdict is no override.
	if err := c.Override(right.ID, classify.Done); err != nil {
		t.Fatal(err)
	}
	s, err := c.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s.Calls != 2 || s.Overrides != 1 {
		t.Errorf("stats %+v, want 2 calls and 1 override", s)
	}
	if err := c.Override("no-such-call", classify.Done); !errors.Is(err, classify.ErrNoCall) {
		t.Errorf("override of an unknown call: %v, want ErrNoCall", err)
	}
	if err := c.Override(right.ID, classify.Unsure); !errors.Is(err, classify.ErrVerdict) {
		t.Errorf("override to unsure: %v, want ErrVerdict", err)
	}
}

func TestTheReplaySetRunsAgainstAnotherProvider(t *testing.T) {
	c, dir := newClassifier(t, &fakeProvider{name: "first", text: "done"})
	asked := c.Classify(context.Background(), turn("Should I open the PR?"))
	c.Classify(context.Background(), turn("All done."))
	if err := c.Override(asked.ID, classify.Asking); err != nil {
		t.Fatal(err)
	}

	// The replay set is on disk: a new Classifier over the same folder
	// reads it.
	again, err := classify.New(&fakeProvider{name: "first"}, dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	other := &fakeProvider{name: "other", answers: map[string]string{
		"Should I open the PR?": "asking",
		"All done.":             "stuck",
	}}
	rep, err := again.Replay(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Cases) != 2 || rep.Agree != 1 {
		t.Fatalf("replay %+v, want 2 cases and 1 agreement", rep)
	}
	for _, rc := range rep.Cases {
		want := map[string]classify.Verdict{"Should I open the PR?": classify.Asking, "All done.": classify.Done}[rc.Message]
		if rc.Want != want {
			t.Errorf("case %q: want %q, expected %q (the override wins over the verdict)", rc.Message, rc.Want, want)
		}
	}
	// A replay measures the other provider; it adds nothing to the set.
	s, err := again.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s.Calls != 2 {
		t.Errorf("calls %d after a replay, want 2", s.Calls)
	}
}

func TestTheCommandProviderReadsClaudeJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake command is a shell script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fakeclaude")
	// Prints what `claude -p --output-format json` prints, and fails the way
	// it does when it can't log in.
	body := `#!/bin/sh
msg=$(cat)
case "$msg" in
  *login*) echo '{"type":"result","is_error":true,"result":"Invalid API key","duration_ms":10,"total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}'; exit 1 ;;
  *) echo '{"type":"result","is_error":false,"result":"asking","duration_ms":800,"total_cost_usd":0.00012,"usage":{"input_tokens":150,"output_tokens":2}}' ;;
esac
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	p := classify.NewCommand("fake claude", []string{script})

	a, err := p.Classify(context.Background(), "Should I open the PR?")
	if err != nil {
		t.Fatal(err)
	}
	if a.Text != "asking" || a.InputTokens != 150 || a.OutputTokens != 2 || a.CostUSD != 0.00012 {
		t.Errorf("answer %+v", a)
	}
	if _, err := p.Classify(context.Background(), "needs a login"); err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error %v, want the CLI's message", err)
	}
}

func TestTheDefaultProviderRunsClaudeOnTheUsersLogin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	bin := t.TempDir()
	// Answers "stuck" when it gets an API key, which would bill the key
	// instead of using the user's Claude login, and records its arguments.
	body := `#!/bin/sh
cat >/dev/null
printf '%s\n' "$@" > "$(dirname "$0")/args"
if [ -n "$ANTHROPIC_API_KEY" ]; then answer=stuck; else answer=done; fi
echo "{\"is_error\":false,\"result\":\"$answer\",\"usage\":{}}"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ANTHROPIC_API_KEY", "your-api-key")

	a, err := classify.NewClaude().Classify(context.Background(), "All done.")
	if err != nil {
		t.Fatal(err)
	}
	if a.Text != "done" {
		t.Errorf("answer %q: claude got ANTHROPIC_API_KEY", a.Text)
	}
	args, err := os.ReadFile(filepath.Join(bin, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "claude-haiku-5-5") || !strings.Contains(string(args), "-p\n") {
		t.Errorf("claude ran with %q, want -p and Haiku", args)
	}
	if !strings.Contains(string(args), "--system-prompt\n"+classify.Instruction) {
		t.Errorf("claude ran with %q, want the instruction as its system prompt", args)
	}
}

func TestALongMessageIsCutToItsEnd(t *testing.T) {
	p := &fakeProvider{name: "fake", text: "asking"}
	c, _ := newClassifier(t, p)
	long := strings.Repeat("ä", classify.MaxMessage) + " Should I open the PR?"
	c.Classify(context.Background(), turn(long))

	if len(p.last) > classify.MaxMessage || !strings.HasSuffix(p.last, "Should I open the PR?") {
		t.Errorf("the provider read %d bytes ending %q, want at most %d ending with the question",
			len(p.last), p.last[max(0, len(p.last)-30):], classify.MaxMessage)
	}
	if !utf8.ValidString(p.last) {
		t.Error("the cut split a character")
	}
}

func TestResolvingAStopPointIsNoOverride(t *testing.T) {
	failing := &fakeProvider{name: "fake", err: errors.New("claude: not logged in")}
	c, _ := newClassifier(t, failing)
	unsure := c.Classify(context.Background(), turn("Should I open the PR?"))
	if err := c.Override(unsure.ID, classify.Asking); err != nil {
		t.Fatal(err)
	}
	s, err := c.Stats()
	if err != nil {
		t.Fatal(err)
	}
	// The classifier didn't claim anything, so deciding it isn't a mistake
	// it made; it is a Stop point the user resolved.
	if s.Overrides != 0 || s.Resolved != 1 {
		t.Errorf("stats %+v, want 0 overrides and 1 resolved", s)
	}
}

func TestAHungCommandEndsWithItsContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake command is a shell script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hangs")
	// A child that keeps stdout open, as an MCP server started by claude
	// might, and a parent that never answers.
	body := "#!/bin/sh\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := classify.NewCommand("hangs", []string{script}).Classify(ctx, "All done.")
	if err == nil {
		t.Fatal("a hung command answered")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Classify returned after %v; the child holding stdout kept it waiting", d)
	}
}

func TestAnyClaudeCommandRunsOnTheUsersLogin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	bin := t.TempDir()
	body := `#!/bin/sh
cat >/dev/null
if [ -n "$ANTHROPIC_API_KEY" ]; then answer=stuck; else answer=done; fi
echo "{\"is_error\":false,\"result\":\"$answer\",\"usage\":{}}"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "your-api-key")

	// A [classifier] command with another model still runs claude.
	p := classify.NewCommand("claude sonnet", []string{filepath.Join(bin, "claude"), "-p", "--model", "claude-sonnet-5-5"})
	a, err := p.Classify(context.Background(), "All done.")
	if err != nil {
		t.Fatal(err)
	}
	if a.Text != "done" {
		t.Errorf("answer %q: a configured claude got ANTHROPIC_API_KEY", a.Text)
	}
}
