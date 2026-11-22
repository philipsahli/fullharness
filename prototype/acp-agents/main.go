// PROTOTYPE — throwaway spike for fullharness. Never merge to main.
//
// Question: can fh drive Claude Code (via claude-agent-acp), Copilot CLI and
// OpenCode over ACP v1 from Go, well enough for ADR 0003 (ACP-first Sessions)
// and ADR 0006 (Child Sessions via fh's MCP tool)? Runs 8 checks per agent in
// a fresh scratch git repo and logs every ACP message to logs/<agent>.jsonl.
//
//	go run . -agent all        # or claude | copilot | opencode
//	go run . -mcp-ping         # (internal) act as the stdio MCP server for check 8
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const (
	pingMarker  = "FH-PING-7731"
	echoMarker  = "ECHO-MARKER-4412"
	pongWord    = "PONG-1"
	permFile    = "fh-perm.txt"
	promptLimit = 4 * time.Minute
)

// ---------- agents ----------

type agentDef struct {
	name string
	cmd  []string
	env  []string // extra env
}

func agents() map[string]agentDef {
	claudePath, _ := exec.LookPath("claude")
	return map[string]agentDef{
		"claude":   {"claude", []string{"npx", "-y", "@agentclientprotocol/claude-agent-acp@0.109.1"}, []string{"CLAUDE_CODE_EXECUTABLE=" + claudePath}},
		"copilot":  {"copilot", []string{"copilot", "--acp", "--stdio"}, nil},
		"opencode": {"opencode", []string{"opencode", "acp"}, nil},
	}
}

// agentEnv strips ANTHROPIC_API_KEY so agents use their own logins, not API billing.
func agentEnv(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// ---------- JSONL logging of raw ACP traffic ----------

type jsonl struct {
	mu sync.Mutex
	f  *os.File
}

func (j *jsonl) write(dir string, line []byte) {
	j.mu.Lock()
	defer j.mu.Unlock()
	rec := map[string]any{"t": time.Now().Format(time.RFC3339Nano), "dir": dir}
	if json.Valid(line) {
		rec["msg"] = json.RawMessage(line)
	} else {
		rec["raw"] = string(line)
	}
	b, _ := json.Marshal(rec)
	j.f.Write(append(b, '\n'))
}

func (j *jsonl) note(format string, a ...any) {
	j.write("note", []byte(fmt.Sprintf("%q", fmt.Sprintf(format, a...))))
}

// lineTap splits a byte stream into lines and logs each one.
type lineTap struct {
	log *jsonl
	dir string
	buf []byte
}

func (t *lineTap) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	for {
		i := strings.IndexByte(string(t.buf), '\n')
		if i < 0 {
			break
		}
		if i > 0 {
			t.log.write(t.dir, append([]byte(nil), t.buf[:i]...))
		}
		t.buf = t.buf[i+1:]
	}
	return len(p), nil
}

// ---------- ACP client (fh's side) ----------

type spikeClient struct {
	log *jsonl

	mu          sync.Mutex
	text        strings.Builder // agent message text of the current prompt turn
	replay      strings.Builder // user+agent text received during session/load
	inLoad      bool
	commands    []string
	permAsked   []string
	permAllowed []string
}

var _ acp.Client = (*spikeClient)(nil)

func (c *spikeClient) resetTurn() {
	c.mu.Lock()
	c.text.Reset()
	c.mu.Unlock()
}

func (c *spikeClient) turnText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text.String()
}

func (c *spikeClient) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	title, _ := p.ToolCall.Title.Value()
	raw, _ := p.ToolCall.RawInput.Value()
	desc := title + " " + string(raw)
	c.mu.Lock()
	c.permAsked = append(c.permAsked, desc)
	c.mu.Unlock()
	// Allow only the two things the scripted prompts ask for; reject anything else.
	allow := strings.Contains(desc, permFile) || strings.Contains(strings.ToLower(desc), "fh_ping") || strings.Contains(strings.ToLower(desc), "fh-ping")
	want := acp.PermissionOptionKindRejectOnce
	if allow {
		want = acp.PermissionOptionKindAllowOnce
	}
	for _, o := range p.Options {
		if o.Kind == want {
			if allow {
				c.mu.Lock()
				c.permAllowed = append(c.permAllowed, desc)
				c.mu.Unlock()
			}
			c.log.note("permission %s: %s", o.Kind, desc)
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId}}}, nil
		}
	}
	c.log.note("permission cancelled (no %s option): %s", want, desc)
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
}

func (c *spikeClient) SessionUpdate(ctx context.Context, n acp.SessionNotification) error {
	u := n.Update
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		t := u.AgentMessageChunk.Content.Text.Text
		c.text.WriteString(t)
		if c.inLoad {
			c.replay.WriteString(t)
		}
	case u.UserMessageChunk != nil && u.UserMessageChunk.Content.Text != nil:
		if c.inLoad {
			c.replay.WriteString(u.UserMessageChunk.Content.Text.Text)
		}
	case u.AvailableCommandsUpdate != nil:
		c.commands = c.commands[:0]
		for _, cmd := range u.AvailableCommandsUpdate.AvailableCommands {
			c.commands = append(c.commands, cmd.Name)
		}
	}
	return nil
}

// fh offers no fs/terminal capabilities (ADR 0003), so these must never be called.
func (c *spikeClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	c.log.note("UNEXPECTED fs/read_text_file")
	return acp.ReadTextFileResponse{}, fmt.Errorf("not supported")
}
func (c *spikeClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	c.log.note("UNEXPECTED fs/write_text_file")
	return acp.WriteTextFileResponse{}, fmt.Errorf("not supported")
}
func (c *spikeClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	c.log.note("UNEXPECTED terminal/create")
	return acp.CreateTerminalResponse{}, fmt.Errorf("not supported")
}
func (c *spikeClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, fmt.Errorf("not supported")
}
func (c *spikeClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, fmt.Errorf("not supported")
}
func (c *spikeClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, fmt.Errorf("not supported")
}
func (c *spikeClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, fmt.Errorf("not supported")
}
func (c *spikeClient) CreateElicitation(context.Context, acp.CreateElicitationRequest) (acp.CreateElicitationResponse, error) {
	c.log.note("elicitation requested; cancelling")
	return acp.CreateElicitationResponse{Cancel: &acp.CreateElicitationCancel{Action: "cancel"}}, nil
}
func (c *spikeClient) CompleteElicitation(context.Context, acp.CompleteElicitationNotification) error {
	return nil
}

// ---------- one running agent process ----------

type run struct {
	def    agentDef
	cmd    *exec.Cmd
	conn   *acp.ClientSideConnection
	client *spikeClient
	init   acp.InitializeResponse
}

func start(ctx context.Context, def agentDef, dir string, log *jsonl, client *spikeClient) (*run, error) {
	cmd := exec.CommandContext(ctx, def.cmd[0], def.cmd[1:]...)
	cmd.Dir = dir
	cmd.Env = agentEnv(def.env)
	stderr, _ := os.Create(filepath.Join("logs", def.name+".stderr.log"))
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out := io.MultiWriter(stdin, &lineTap{log: log, dir: "out"})
	in := io.TeeReader(stdout, &lineTap{log: log, dir: "in"})
	conn := acp.NewClientSideConnection(client, writerOnly{out}, in)
	ictx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// Empty client capabilities on purpose: check 5.
	resp, err := conn.Initialize(ictx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber, ClientCapabilities: acp.ClientCapabilities{}})
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	return &run{def: def, cmd: cmd, conn: conn, client: client, init: resp}, nil
}

type writerOnly struct{ io.Writer }

func (r *run) stop() {
	_ = r.cmd.Process.Kill()
	_ = r.cmd.Wait()
}

func (r *run) prompt(ctx context.Context, sid acp.SessionId, text string) (acp.StopReason, string, error) {
	r.client.resetTurn()
	pctx, cancel := context.WithTimeout(ctx, promptLimit)
	defer cancel()
	resp, err := r.conn.Prompt(pctx, acp.PromptRequest{SessionId: sid, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
	time.Sleep(300 * time.Millisecond) // let trailing chunks arrive
	return resp.StopReason, r.client.turnText(), err
}

// ---------- checks ----------

type result struct {
	pass bool
	note string
}

type report struct {
	agent   string
	info    string
	results [9]result // index 1..8
}

var (
	mcpMode = flag.String("mcp", "http", "how check 8 passes the MCP server: http | stdio")
	mcpURL  string // set when the in-process HTTP MCP server starts
)

func mcpServers(self string) []acp.McpServer {
	if *mcpMode == "stdio" {
		return []acp.McpServer{{Stdio: &acp.McpServerStdio{Name: "fh-ping", Command: self, Args: []string{"-mcp-ping"}, Env: []acp.EnvVariable{}}}}
	}
	return []acp.McpServer{{Http: &acp.McpServerHttpInline{Type: "http", Name: "fh-ping", Url: mcpURL, Headers: []acp.HttpHeader{}}}}
}

// setAskMode puts the Session into the mode where the agent asks before acting.
// Claude Code otherwise inherits the user's own permission mode (here: auto).
func setAskMode(ctx context.Context, r *run, sid acp.SessionId, log *jsonl) {
	if r.def.name != "claude" {
		return
	}
	mctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := r.conn.SetSessionMode(mctx, acp.SetSessionModeRequest{SessionId: sid, ModeId: "default"})
	log.note("session/set_mode default: err=%v", err)
}

func runAgent(ctx context.Context, def agentDef, self string) report {
	rep := report{agent: def.name}
	f, _ := os.Create(filepath.Join("logs", def.name+".jsonl"))
	defer f.Close()
	log := &jsonl{f: f}
	set := func(i int, pass bool, note string, a ...any) {
		rep.results[i] = result{pass, fmt.Sprintf(note, a...)}
		log.note("check %d %v: %s", i, pass, rep.results[i].note)
	}

	dir, err := scratchRepo(def.name)
	if err != nil {
		set(1, false, "scratch repo: %v", err)
		return rep
	}
	log.note("scratch repo %s", dir)

	client := &spikeClient{log: log}
	r, err := start(ctx, def, dir, log, client)
	if err != nil {
		set(1, false, "start: %v", err)
		return rep
	}
	ai := ""
	if r.init.AgentInfo != nil {
		ai = r.init.AgentInfo.Name + " " + r.init.AgentInfo.Version
	}
	caps := r.init.AgentCapabilities
	rep.info = fmt.Sprintf("%s; protocol v%v; loadSession=%v; mcp http=%v sse=%v", ai, r.init.ProtocolVersion, caps.LoadSession, caps.McpCapabilities.Http, caps.McpCapabilities.Sse)

	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	sess, err := r.conn.NewSession(sctx, acp.NewSessionRequest{Cwd: dir, McpServers: mcpServers(self)})
	cancel()
	if err != nil {
		set(1, false, "session/new: %v", err)
		r.stop()
		return rep
	}
	sid := sess.SessionId
	setAskMode(ctx, r, sid, log)

	// 1 Drive over ACP
	stop, text, err := r.prompt(ctx, sid, "Reply with exactly the word "+pongWord+" and nothing else. Do not use any tools.")
	set(1, err == nil && stop == acp.StopReasonEndTurn && strings.Contains(text, pongWord), "stop=%q err=%v reply=%q", stop, err, clip(text))
	if !rep.results[1].pass {
		r.stop()
		return rep
	}
	if def.name == "claude" {
		set(6, true, "check 1 passed through claude-agent-acp")
	}

	// 2 + 7 Slash skills / skill location
	client.mu.Lock()
	listed := contains(client.commands, "fh-spike-echo")
	cmds := strings.Join(client.commands, ",")
	client.mu.Unlock()
	stop, text, err = r.prompt(ctx, sid, "/fh-spike-echo")
	ran := err == nil && strings.Contains(text, echoMarker)
	set(2, listed && ran, "listed=%v ran=%v stop=%q commands=[%s] reply=%q", listed, ran, stop, clip(cmds), clip(text))
	set(7, ran, "skill from .claude/skills/ produced marker=%v", ran)

	// 3 Waiting: permission request + end_turn
	client.mu.Lock()
	before := len(client.permAsked)
	client.mu.Unlock()
	stop, text, err = r.prompt(ctx, sid, "Use your shell tool to run exactly this command in the current directory: touch "+permFile+" . Then reply DONE.")
	client.mu.Lock()
	asked := len(client.permAsked) > before
	client.mu.Unlock()
	_, ferr := os.Stat(filepath.Join(dir, permFile))
	set(3, asked && err == nil && stop == acp.StopReasonEndTurn, "permission_requested=%v stop=%q file_created=%v reply=%q", asked, stop, ferr == nil, clip(text))

	// 8 MCP pass-through
	stop, text, err = r.prompt(ctx, sid, "Call the fh_ping tool from the fh-ping MCP server and reply with the exact text it returned, nothing else.")
	set(8, err == nil && strings.Contains(text, pingMarker), "stop=%q reply=%q", stop, clip(text))

	// 4 Reopen: kill, restart, session/load
	r.stop()
	if !caps.LoadSession {
		set(4, false, "agent does not advertise loadSession")
	} else {
		client2 := &spikeClient{log: log}
		r2, err := start(ctx, def, dir, log, client2)
		if err != nil {
			set(4, false, "restart: %v", err)
		} else {
			client2.mu.Lock()
			client2.inLoad = true
			client2.mu.Unlock()
			lctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			_, lerr := r2.conn.LoadSession(lctx, acp.LoadSessionRequest{SessionId: sid, Cwd: dir, McpServers: mcpServers(self)})
			cancel()
			setAskMode(ctx, r2, sid, log)
			time.Sleep(time.Second)
			client2.mu.Lock()
			client2.inLoad = false
			replayed := strings.Contains(client2.replay.String(), pongWord)
			client2.mu.Unlock()
			remembered := false
			if lerr == nil {
				_, t, perr := r2.prompt(ctx, sid, "Which exact word did you reply with in your first answer in this conversation? Reply with just that word.")
				remembered = perr == nil && strings.Contains(t, pongWord)
			}
			set(4, lerr == nil && (replayed || remembered), "load_err=%v history_replayed=%v remembered=%v", lerr, replayed, remembered)
			r2.stop()
		}
	}

	// 5 No fs/terminal: 1-4 passed with empty clientCapabilities
	ok5 := rep.results[1].pass && rep.results[2].pass && rep.results[3].pass && rep.results[4].pass
	set(5, ok5, "checks 1-4 all passed with empty clientCapabilities: %v", ok5)
	return rep
}

func scratchRepo(agent string) (string, error) {
	dir, err := os.MkdirTemp("", "fh-spike-"+agent+"-")
	if err != nil {
		return "", err
	}
	files := map[string]string{
		"README.md": "# fh spike scratch repo\n\nPROTOTYPE, wipe me.\n",
		"main.go":   "package main\n\nfunc main() {}\n",
		".claude/skills/fh-spike-echo/SKILL.md": "---\nname: fh-spike-echo\ndescription: Test skill for the fullharness ACP spike. Use when the user types /fh-spike-echo.\n---\n\nReply with exactly `" + echoMarker + "` and nothing else. Do not use any tools.\n",
		"opencode.json": `{"$schema":"https://opencode.ai/config.json","model":"github-copilot/claude-haiku-5.5","permission":{"bash":"ask","edit":"ask"}}` + "\n",
	}
	for p, c := range files {
		fp := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(fp), 0o755)
		if err := os.WriteFile(fp, []byte(c), 0o644); err != nil {
			return "", err
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=fh-spike", "-c", "user.email=spike@example.invalid", "commit", "-qm", "scratch"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %v: %v %s", args, err, out)
		}
	}
	return dir, nil
}

// ---------- minimal stdio MCP server for check 8 ----------

func mcpPing() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		if resp := mcpHandle(in.Bytes()); resp != nil {
			os.Stdout.Write(append(resp, '\n'))
		}
	}
}

// serveMCPHTTP runs the same tool as an MCP Streamable-HTTP server (JSON responses only).
func serveMCPHTTP() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		resp := mcpHandle(body)
		if resp == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(resp)
	})
	go http.Serve(ln, mux)
	return "http://" + ln.Addr().String() + "/mcp"
}

// mcpHandle answers one JSON-RPC message; nil for notifications.
func mcpHandle(line []byte) []byte {
	{
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(line, &req) != nil || req.ID == nil {
			return nil
		}
		var result any
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(req.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			result = map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fh-ping", "version": "0.0.1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "fh_ping", "description": "Returns a fixed marker string. Used by the fullharness spike.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": pingMarker}}}
		case "ping":
			result = map[string]any{}
		default:
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
			return b
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		return b
	}
}

// ---------- main ----------

func main() {
	agent := flag.String("agent", "all", "claude | copilot | opencode | all")
	ping := flag.Bool("mcp-ping", false, "act as the stdio MCP server for check 8")
	flag.Parse()
	if *ping {
		mcpPing()
		return
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.MkdirAll("logs", 0o755)
	if *mcpMode == "http" {
		mcpURL = serveMCPHTTP()
		fmt.Println("MCP test server:", mcpURL)
	}
	defs := agents()
	names := []string{"claude", "copilot", "opencode"}
	if *agent != "all" {
		names = []string{*agent}
	}
	ctx := context.Background()
	var reps []report
	for _, n := range names {
		fmt.Printf("== %s\n", n)
		rep := runAgent(ctx, defs[n], self)
		fmt.Printf("   %s\n", rep.info)
		for i := 1; i <= 8; i++ {
			fmt.Printf("   %d %-4s %s\n", i, mark(rep.results[i]), rep.results[i].note)
		}
		reps = append(reps, rep)
	}
	fmt.Println("\ncheck                 " + strings.Join(names, "  "))
	labels := []string{"", "1 drive over ACP", "2 slash skills", "3 waiting", "4 reopen", "5 no fs/terminal", "6 claude adapter", "7 skill location", "8 MCP pass-through"}
	for i := 1; i <= 8; i++ {
		row := fmt.Sprintf("%-22s", labels[i])
		for _, r := range reps {
			row += fmt.Sprintf("%-*s", len(r.agent)+2, mark(r.results[i]))
		}
		fmt.Println(row)
	}
}

func mark(r result) string {
	switch {
	case r.note == "":
		return "-"
	case r.pass:
		return "PASS"
	default:
		return "FAIL"
	}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x || strings.TrimPrefix(v, "/") == x {
			return true
		}
	}
	return false
}
