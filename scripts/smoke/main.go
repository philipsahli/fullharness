// Command smoke is the release smoke test (make smoke, #16): it runs the ACP
// spike's checks against the real Claude Code, Copilot CLI and OpenCode,
// each in a fresh scratch git repo, through fh's own Agent definitions and
// ACP client. It needs the Agents installed and logged in, and spends real
// tokens, so it never runs in CI (ADR 0007). It exits 1 if any check fails.
//
//	go run ./scripts/smoke                  # all three Agents
//	go run ./scripts/smoke -agent opencode  # one Agent
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	sdk "github.com/coder/acp-go-sdk"

	"github.com/philipsahli/fullharness/internal/acp"
	"github.com/philipsahli/fullharness/internal/agent"
)

const (
	pongWord   = "PONG-1"
	echoMarker = "FH-SMOKE-ECHO-4412"
	skillName  = "fh-smoke-echo"
	permFile   = "fh-perm.txt"
	// The first Claude Code start downloads its adapter through npx.
	startLimit  = 2 * time.Minute
	promptLimit = 4 * time.Minute
)

var builtins = []string{"claude", "copilot", "opencode"}

// The checks, numbered as in the spike's FINDINGS.md. The spike's check 8
// (MCP pass-through) is post-v1 (ADR 0006); 8 here is the one spec #1 adds:
// OpenCode asks because of OPENCODE_PERMISSION, an undocumented variable.
var labels = [...]string{
	1: "drive over ACP",
	2: "slash skills",
	3: "permission and turn end",
	4: "reopen",
	5: "no fs/terminal",
	6: "claude adapter",
	7: ".claude/skills/ loading",
	8: "OPENCODE_PERMISSION",
}

type result struct {
	ran  bool
	pass bool
	note string
}

type report struct {
	agent   string
	results [len(labels)]result
}

func (r *report) failed() bool {
	return slices.ContainsFunc(r.results[:], func(x result) bool { return x.ran && !x.pass })
}

func main() {
	only := flag.String("agent", "all", "claude | copilot | opencode | all")
	configPath := flag.String("config", "", "a file with [agents.<id>] overrides, as in config.toml (default: the built-in definitions; the tests point Agents at a fake)")
	flag.Parse()

	var cfg struct {
		Agents map[string]agent.Override `toml:"agents"`
	}
	if *configPath != "" {
		if _, err := toml.DecodeFile(*configPath, &cfg); err != nil {
			fmt.Fprintln(os.Stderr, "smoke:", err)
			os.Exit(2)
		}
	}
	names := builtins
	if *only != "all" {
		if !slices.Contains(builtins, *only) {
			fmt.Fprintf(os.Stderr, "smoke: unknown agent %q\n", *only)
			os.Exit(2)
		}
		names = []string{*only}
	}
	logDir, err := os.MkdirTemp("", "fh-smoke-logs-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "smoke:", err)
		os.Exit(2)
	}

	defs := agent.Resolve(cfg.Agents)
	var reps []report
	for _, name := range names {
		i := slices.IndexFunc(defs, func(d agent.Definition) bool { return d.ID == name })
		def := defs[i]
		fmt.Printf("== %s (%s)\n", def.Name, strings.Join(def.Command, " "))
		rep := runAgent(def, logDir)
		for n := 1; n < len(labels); n++ {
			if r := rep.results[n]; r.ran {
				fmt.Printf("   %d %s %s: %s\n", n, mark(r), labels[n], r.note)
			}
		}
		reps = append(reps, rep)
	}

	fmt.Printf("\n%-26s", "check")
	for _, r := range reps {
		fmt.Printf("%-10s", r.agent)
	}
	fmt.Println()
	failed := false
	for n := 1; n < len(labels); n++ {
		fmt.Printf("%-26s", fmt.Sprintf("%d %s", n, labels[n]))
		for _, r := range reps {
			fmt.Printf("%-10s", mark(r.results[n]))
		}
		fmt.Println()
	}
	for _, r := range reps {
		failed = failed || r.failed()
	}
	fmt.Println("\nAgent stderr logs:", logDir)
	if failed {
		fmt.Println("smoke: FAIL")
		os.Exit(1)
	}
	fmt.Println("smoke: PASS")
}

func mark(r result) string {
	switch {
	case !r.ran:
		return "-"
	case r.pass:
		return "PASS"
	default:
		return "FAIL"
	}
}

// observer is fh's side of one Agent process: it collects what the checks
// look at and answers permission requests.
type observer struct {
	mu       sync.Mutex
	text     strings.Builder // the current turn's reply
	replay   strings.Builder // text replayed during session/load
	inLoad   bool
	commands []string
	asked    int
	refused  []string
}

func (o *observer) Update(n sdk.SessionNotification) {
	o.mu.Lock()
	defer o.mu.Unlock()
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		o.text.WriteString(u.AgentMessageChunk.Content.Text.Text)
		if o.inLoad {
			o.replay.WriteString(u.AgentMessageChunk.Content.Text.Text)
		}
	case u.UserMessageChunk != nil && u.UserMessageChunk.Content.Text != nil:
		if o.inLoad {
			o.replay.WriteString(u.UserMessageChunk.Content.Text.Text)
		}
	case u.AvailableCommandsUpdate != nil:
		o.commands = o.commands[:0]
		for _, c := range u.AvailableCommandsUpdate.AvailableCommands {
			o.commands = append(o.commands, c.Name)
		}
	}
}

// Permission allows only the scratch-file command check 3 asks for, and
// rejects anything else an Agent might try in the scratch repo.
func (o *observer) Permission(_ context.Context, r sdk.RequestPermissionRequest) sdk.RequestPermissionResponse {
	title, _ := r.ToolCall.Title.Value()
	raw, _ := r.ToolCall.RawInput.Value()
	o.mu.Lock()
	o.asked++
	o.mu.Unlock()
	want := sdk.PermissionOptionKindRejectOnce
	if strings.Contains(title+string(raw), permFile) {
		want = sdk.PermissionOptionKindAllowOnce
	}
	for _, opt := range r.Options {
		if opt.Kind == want {
			return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{Selected: &sdk.RequestPermissionOutcomeSelected{OptionId: opt.OptionId}}}
		}
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{Cancelled: &sdk.RequestPermissionOutcomeCancelled{}}}
}

func (o *observer) Refused(method string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.refused = append(o.refused, method)
}

// agentRun is one Agent process in the scratch repo.
type agentRun struct {
	def  agent.Definition
	proc *acp.Process
	obs  *observer
}

func start(def agent.Definition, dir string, stderr *os.File) (*agentRun, bool, error) {
	obs := &observer{}
	quiet := slog.New(slog.NewTextHandler(stderr, nil))
	proc, err := acp.Start(def.Command, def.Environ(false), dir, stderr, quiet, obs)
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), startLimit)
	defer cancel()
	canLoad, err := proc.Initialize(ctx)
	if err != nil {
		proc.Stop()
		return nil, false, fmt.Errorf("initialize: %w", err)
	}
	return &agentRun{def: def, proc: proc, obs: obs}, canLoad, nil
}

// prompt sends text and returns the stop reason and the turn's reply.
func (r *agentRun) prompt(sid sdk.SessionId, text string) (sdk.StopReason, string, error) {
	r.obs.mu.Lock()
	r.obs.text.Reset()
	r.obs.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), promptLimit)
	defer cancel()
	stop, err := r.proc.Prompt(ctx, sid, text)
	time.Sleep(300 * time.Millisecond) // let trailing chunks arrive
	r.obs.mu.Lock()
	defer r.obs.mu.Unlock()
	return stop, r.obs.text.String(), err
}

// setAsking makes the Agent ask before acting, the way fh does for a Session
// without auto-approve (the environment part is set at launch).
func (r *agentRun) setAsking(s acp.Session) error {
	a := r.def.Asking
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if a.Mode.Ask != "" {
		if !slices.Contains(s.Modes, a.Mode.Ask) {
			return fmt.Errorf("mode %q not offered (offered: %v)", a.Mode.Ask, s.Modes)
		}
		if err := r.proc.SetMode(ctx, s.ID, a.Mode.Ask); err != nil {
			return fmt.Errorf("set mode %s: %w", a.Mode.Ask, err)
		}
	}
	if a.Option != "" {
		if !slices.Contains(s.Options[a.Option], a.Value.Ask) {
			return fmt.Errorf("option %s=%s not offered (offered: %v)", a.Option, a.Value.Ask, s.Options[a.Option])
		}
		if err := r.proc.SetOption(ctx, s.ID, a.Option, a.Value.Ask); err != nil {
			return fmt.Errorf("set option %s: %w", a.Option, err)
		}
	}
	return nil
}

func runAgent(def agent.Definition, logDir string) report {
	rep := report{agent: def.ID}
	set := func(n int, pass bool, format string, a ...any) {
		rep.results[n] = result{ran: true, pass: pass, note: fmt.Sprintf(format, a...)}
	}
	// failRest marks every check that applies to this Agent as failed.
	failRest := func(why string) {
		for n := 1; n < len(labels); n++ {
			if !rep.results[n].ran && applies(def.ID, n) {
				set(n, false, "%s", why)
			}
		}
	}
	if missing := def.Missing(); missing != "" {
		failRest(missing)
		return rep
	}
	dir, err := scratchRepo(def.ID)
	if err != nil {
		failRest("scratch repo: " + err.Error())
		return rep
	}
	defer func() { _ = os.RemoveAll(dir) }() // a leftover temp dir is harmless
	stderr, err := os.Create(filepath.Join(logDir, def.ID+".stderr.log"))
	if err != nil {
		failRest(err.Error())
		return rep
	}
	defer func() { _ = stderr.Close() }() // only written to

	r, canLoad, err := start(def, dir, stderr)
	if err != nil {
		failRest("start: " + err.Error())
		return rep
	}
	ctx, cancel := context.WithTimeout(context.Background(), startLimit)
	sess, err := r.proc.NewSession(ctx, dir)
	cancel()
	if err != nil {
		r.proc.Stop()
		failRest("session/new: " + err.Error())
		return rep
	}
	askErr := r.setAsking(sess)

	// 1 Drive over ACP
	stop, text, err := r.prompt(sess.ID, "Reply with exactly the word "+pongWord+" and nothing else. Do not use any tools.")
	set(1, err == nil && stop == sdk.StopReasonEndTurn && strings.Contains(text, pongWord), "stop=%q err=%v reply=%q", stop, err, clip(text))
	if !rep.results[1].pass {
		if def.ID == "opencode" && err != nil {
			rep.results[1].note += "; OpenCode over ACP hangs when its config sets no model (README, Agents)"
		}
		r.proc.Stop()
		failRest("skipped: check 1 failed")
		return rep
	}
	if def.ID == "claude" {
		set(6, true, "check 1 passed through %s", strings.Join(def.Command, " "))
	}

	// 3 A permission request, then the turn ends. It runs before the skill
	// check: the skill says "reply with exactly <marker>", and Copilot CLI
	// kept doing that on the next turn instead of running the command.
	r.obs.mu.Lock()
	before := r.obs.asked
	r.obs.mu.Unlock()
	stop, text, err = r.prompt(sess.ID, "Use your shell tool to run exactly this command in the current directory: touch "+permFile+" . Then reply DONE.")
	r.obs.mu.Lock()
	asked := r.obs.asked > before
	r.obs.mu.Unlock()
	_, ferr := os.Stat(filepath.Join(dir, permFile))
	set(3, askErr == nil && asked && err == nil && stop == sdk.StopReasonEndTurn,
		"asking setup err=%v permission_requested=%v stop=%q err=%v file_created=%v reply=%q", askErr, asked, stop, err, ferr == nil, clip(text))
	if def.ID == "opencode" {
		v := def.Environ(false)
		set(8, asked, "started with %s; permission_requested=%v", lastEnv(v, "OPENCODE_PERMISSION"), asked)
	}

	// 2 and 7: the scratch repo's .claude/skills/ skill is listed and runs
	r.obs.mu.Lock()
	listed := slices.ContainsFunc(r.obs.commands, func(c string) bool {
		c = strings.TrimPrefix(c, "/")
		return c == skillName || strings.HasSuffix(c, ":"+skillName)
	})
	r.obs.mu.Unlock()
	stop, text, err = r.prompt(sess.ID, "/"+skillName)
	ran := err == nil && strings.Contains(text, echoMarker)
	set(2, listed && ran, "listed=%v ran=%v stop=%q reply=%q", listed, ran, stop, clip(text))
	set(7, ran, "skill from .claude/skills/ produced its marker: %v", ran)

	// 4 Reopen: stop the Agent, start it again, session/load
	r.proc.Stop()
	if !canLoad {
		set(4, false, "the Agent doesn't offer session/load")
	} else {
		pass, note := reopen(def, dir, stderr, sess.ID)
		set(4, pass, "%s", note)
	}

	// 5 Checks 1-4 passed without fs/terminal, and none was asked for
	var refused []string
	r.obs.mu.Lock()
	for _, m := range r.obs.refused {
		if strings.HasPrefix(m, "fs/") || strings.HasPrefix(m, "terminal/") {
			refused = append(refused, m)
		}
	}
	r.obs.mu.Unlock()
	ok := rep.results[1].pass && rep.results[2].pass && rep.results[3].pass && rep.results[4].pass
	set(5, ok && len(refused) == 0, "checks 1-4 passed=%v; fs/terminal requests refused=%v", ok, refused)
	return rep
}

// applies reports whether check n runs for agent id.
func applies(id string, n int) bool {
	switch n {
	case 6:
		return id == "claude"
	case 8:
		return id == "opencode"
	}
	return true
}

// reopen runs check 4 in a new process: session/load must replay the first
// turn, or the Agent must still remember it.
func reopen(def agent.Definition, dir string, stderr *os.File, sid sdk.SessionId) (bool, string) {
	r, _, err := start(def, dir, stderr)
	if err != nil {
		return false, "restart: " + err.Error()
	}
	defer r.proc.Stop()
	r.obs.mu.Lock()
	r.obs.inLoad = true
	r.obs.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), startLimit)
	sess, err := r.proc.LoadSession(ctx, sid, dir)
	cancel()
	if err != nil {
		return false, "session/load: " + err.Error()
	}
	time.Sleep(time.Second) // let the replay's last chunks arrive
	r.obs.mu.Lock()
	r.obs.inLoad = false
	replayed := strings.Contains(r.obs.replay.String(), pongWord)
	r.obs.mu.Unlock()
	askErr := r.setAsking(sess)
	remembered := false
	if !replayed {
		_, text, err := r.prompt(sid, "Which exact word did you reply with in your first answer in this conversation? Reply with just that word.")
		remembered = err == nil && strings.Contains(text, pongWord)
	}
	return askErr == nil && (replayed || remembered), fmt.Sprintf("history_replayed=%v remembered=%v asking setup err=%v", replayed, remembered, askErr)
}

// lastEnv returns the last NAME=value for name in env, as a process sees it.
func lastEnv(env []string, name string) string {
	v := name + " unset"
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			v = kv
		}
	}
	return v
}

func scratchRepo(id string) (string, error) {
	dir, err := os.MkdirTemp("", "fh-smoke-"+id+"-")
	if err != nil {
		return "", err
	}
	// No opencode.json: OpenCode must ask because of OPENCODE_PERMISSION
	// alone (check 8), as in a user's repo.
	files := map[string]string{
		"README.md": "# fh smoke-test scratch repo\n",
		"main.go":   "package main\n\nfunc main() {}\n",
		".claude/skills/" + skillName + "/SKILL.md": "---\nname: " + skillName + "\ndescription: Test skill for fh's release smoke test. Use when the user types /" + skillName + ".\n---\n\nReply with exactly `" + echoMarker + "` and nothing else. Do not use any tools.\n",
	}
	for p, c := range files {
		fp := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(fp, []byte(c), 0o644); err != nil {
			return "", err
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=fh-smoke", "-c", "user.email=smoke@example.invalid", "commit", "-qm", "scratch"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %v: %v %s", args, err, out)
		}
	}
	return dir, nil
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
