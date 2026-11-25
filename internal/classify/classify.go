// Package classify decides how a Session's turn ended, for Autopilot
// (ADR-0010): is the Agent asking the user something, done, or stuck? Rules
// decide when they can; otherwise a Provider reads the Agent's last message.
// Every Provider call is recorded, with its latency and cost, in a replay
// set that also counts the user's overrides.
package classify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxMessage caps how much of the Agent's last message the provider reads
// and the replay set keeps. A turn's question or result is at its end, so
// the end is kept. One unbounded message would also make the set unreadable.
const MaxMessage = 8 << 10

// Timeout bounds one provider call. A hung call (a login prompt, no
// network) becomes Unsure, a Stop point, instead of holding the turn end.
const Timeout = time.Minute

// Verdict is how a turn ended.
type Verdict string

const (
	Asking Verdict = "asking" // the Agent asks a question or waits for a decision
	Done   Verdict = "done"   // the Agent finished and needs nothing
	Stuck  Verdict = "stuck"  // the Agent failed or can't go on
	// Unsure: no rule decided and the Provider failed or gave another
	// answer. Autopilot stops and asks the user.
	Unsure Verdict = "unsure"
)

// Input is one turn end.
type Input struct {
	Session string
	// StopReason is ACP's stop reason (end_turn, cancelled, max_tokens,
	// refusal, ...); empty when Err is set.
	StopReason string
	Err        string // the Agent refused the turn with this error
	// PendingPermission: the Session still waits on a permission request.
	PendingPermission bool
	Message           string // the Agent's last message in the turn
}

// Result is a classified turn end.
type Result struct {
	Verdict Verdict
	// By is "rule:<name>" or the Provider's name.
	By string
	// ID names the recorded Provider call, for Override; empty when a rule
	// decided.
	ID string
}

// StopPoint reports whether Autopilot waits for the user here: everything
// but done does.
func (r Result) StopPoint() bool { return r.Verdict != Done }

// Answer is what a Provider said, with what it reports of its cost; zero
// where it reports nothing.
type Answer struct {
	Text         string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// Provider classifies an Agent's last message. Its answer should be one of
// asking, done or stuck; Classifier checks.
type Provider interface {
	Name() string
	Classify(ctx context.Context, message string) (Answer, error)
}

// Errors Override returns.
var (
	ErrNoCall  = errors.New("no such classifier call")
	ErrVerdict = errors.New("an override is asking, done or stuck")
)

// Classifier is the rules plus one Provider, recording each Provider call.
type Classifier struct {
	provider Provider
	set      *replaySet
	log      *slog.Logger
}

// New returns a Classifier that records its calls in dir (fh's local
// state folder) and logs what it can't record to log.
func New(p Provider, dir string, log *slog.Logger) (*Classifier, error) {
	set, err := openReplaySet(dir)
	if err != nil {
		return nil, err
	}
	return &Classifier{provider: p, set: set, log: log}, nil
}

// Classify decides how the turn ended. It never fails: whatever goes wrong
// is Unsure, a Stop point.
func (c *Classifier) Classify(ctx context.Context, in Input) Result {
	if v, rule := decideByRule(in); rule != "" {
		return Result{Verdict: v, By: "rule:" + rule}
	}
	message := tail(in.Message, MaxMessage)
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	start := time.Now()
	a, err := c.provider.Classify(ctx, message)
	if parent.Err() != nil {
		// The caller gave up (fh is closing): there's no answer to measure,
		// and the record is left alone (#68).
		return Result{Verdict: Unsure, By: c.provider.Name()}
	}
	call := call{
		ID:           newID(),
		Time:         start.UTC(),
		Session:      in.Session,
		Provider:     c.provider.Name(),
		Message:      message,
		Answer:       a.Text,
		Verdict:      parse(a.Text),
		LatencyMS:    time.Since(start).Milliseconds(),
		InputTokens:  a.InputTokens,
		OutputTokens: a.OutputTokens,
		CostUSD:      a.CostUSD,
	}
	if err != nil {
		call.Verdict, call.Error = Unsure, err.Error()
	}
	if call.LatencyMS == 0 {
		call.LatencyMS = 1 // under a millisecond still took time; 0 reads as unmeasured
	}
	// A record that can't be written loses a measurement, not the verdict.
	if err := c.set.addCall(call); err != nil {
		c.log.Info("record classifier call failed", "err", err)
	}
	return Result{Verdict: call.Verdict, By: call.Provider, ID: call.ID}
}

// decideByRule returns the verdict and the rule's name, or "" when no rule
// decides. These are facts about the turn; Autopilot adds its own about the
// tracker (a new Spec, an open PR) before it asks the Classifier.
func decideByRule(in Input) (Verdict, string) {
	switch {
	case in.PendingPermission:
		return Asking, "permission"
	case in.Err != "":
		return Stuck, "error"
	case in.StopReason == "cancelled":
		return Asking, "cancelled" // the user stopped the turn and takes over
	case in.StopReason == "refusal" || in.StopReason == "max_tokens":
		return Stuck, in.StopReason
	case strings.TrimSpace(in.Message) == "":
		return Unsure, "no-message"
	}
	return "", ""
}

// tail returns the last max bytes of s, from a rune boundary.
func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[len(s)-max:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// parse reads a provider's answer: one of the three words, any case, with
// surrounding space and a full stop allowed; anything else is Unsure.
func parse(text string) Verdict {
	v := Verdict(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(text), ".")))
	if v.valid() {
		return v
	}
	return Unsure
}

// valid reports whether v is one of the three answers; Unsure is not one.
func (v Verdict) valid() bool { return v == Asking || v == Done || v == Stuck }

// Provider names the Classifier's Provider.
func (c *Classifier) Provider() string { return c.provider.Name() }

// Override records that the user decided the turn end differently, or
// confirms the verdict when v equals it.
func (c *Classifier) Override(id string, v Verdict) error {
	if !v.valid() {
		return ErrVerdict
	}
	return c.set.addOverride(id, v)
}

// Stats sums up the recorded calls.
type Stats struct {
	Calls         int     `json:"calls"`
	Errors        int     `json:"errors"`    // the Provider failed
	Unsure        int     `json:"unsure"`    // includes Errors
	Overrides     int     `json:"overrides"` // the user decided differently from a definite verdict
	Resolved      int     `json:"resolved"`  // Stop points on unsure calls the user decided; no override
	MeanLatencyMS int64   `json:"meanLatencyMs"`
	InputTokens   int     `json:"inputTokens"`
	OutputTokens  int     `json:"outputTokens"`
	CostUSD       float64 `json:"costUsd"`
}

// Stats reads the replay set and sums it up.
func (c *Classifier) Stats() (Stats, error) {
	calls, err := c.set.read()
	if err != nil {
		return Stats{}, err
	}
	var s Stats
	var latency int64
	for _, rec := range calls {
		s.Calls++
		if rec.Error != "" {
			s.Errors++
		}
		if rec.Verdict == Unsure {
			s.Unsure++
		}
		switch {
		case rec.Verdict == Unsure && rec.Override != "":
			s.Resolved++
		case rec.want() != rec.Verdict:
			s.Overrides++
		}
		latency += rec.LatencyMS
		s.InputTokens += rec.InputTokens
		s.OutputTokens += rec.OutputTokens
		s.CostUSD += rec.CostUSD
	}
	if s.Calls > 0 {
		s.MeanLatencyMS = latency / int64(s.Calls)
	}
	return s, nil
}

// ReplayCase is one recorded message run through another Provider.
type ReplayCase struct {
	ID      string
	Message string
	Want    Verdict // the user's override, else the recorded verdict
	Got     Verdict
}

// Replay is how another Provider does on the replay set.
type Replay struct {
	Provider string
	Cases    []ReplayCase
	Agree    int
}

// Replay runs every recorded message with a definite verdict through p and
// compares, to measure p before switching to it. It records nothing.
func (c *Classifier) Replay(ctx context.Context, p Provider) (Replay, error) {
	calls, err := c.set.read()
	if err != nil {
		return Replay{}, err
	}
	rep := Replay{Provider: p.Name()}
	for _, rec := range calls {
		want := rec.want()
		if want == Unsure {
			continue // nothing to compare against
		}
		got := Unsure
		callCtx, cancel := context.WithTimeout(ctx, Timeout)
		if a, err := p.Classify(callCtx, rec.Message); err == nil {
			got = parse(a.Text)
		}
		cancel()
		rep.Cases = append(rep.Cases, ReplayCase{ID: rec.ID, Message: rec.Message, Want: want, Got: got})
		if got == want {
			rep.Agree++
		}
	}
	return rep, nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return hex.EncodeToString(b)
}
