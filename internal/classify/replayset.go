package classify

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// setFile is the replay set. It sits next to state.json rather than in it:
// it grows with every unclear turn end, and holds Agent messages from the
// user's repos, which go nowhere but to the configured Provider (ADR-0010).
const setFile = "classifier.jsonl"

// call is one Provider call. Override is filled in from later override
// lines when the log is read.
type call struct {
	ID           string    `json:"id"`
	Time         time.Time `json:"time"`
	Session      string    `json:"session,omitempty"`
	Provider     string    `json:"provider"`
	Message      string    `json:"message"`
	Answer       string    `json:"answer"`
	Verdict      Verdict   `json:"verdict"`
	Error        string    `json:"error,omitempty"`
	LatencyMS    int64     `json:"latencyMs"`
	InputTokens  int       `json:"inputTokens,omitempty"`
	OutputTokens int       `json:"outputTokens,omitempty"`
	CostUSD      float64   `json:"costUsd,omitempty"`
	Override     Verdict   `json:"-"`
}

// want is the right verdict as far as fh knows: the user's override, else
// the recorded one.
func (c call) want() Verdict {
	if c.Override != "" {
		return c.Override
	}
	return c.Verdict
}

// lineKind says what a line of the log holds.
type lineKind string

const (
	callLine     lineKind = "call"
	overrideLine lineKind = "override"
)

// line is one line of the log: a call, or an override of an earlier one.
// Appending an override, not rewriting the call, keeps the file
// append-only.
type line struct {
	Kind      lineKind  `json:"kind"`
	Call      *call     `json:"call,omitempty"`
	Overrides string    `json:"overrides,omitempty"` // the call's ID, for an override
	Override  Verdict   `json:"override,omitempty"`
	At        time.Time `json:"at,omitzero"`
}

type replaySet struct {
	file string
	mu   sync.Mutex
}

func openReplaySet(dir string) (*replaySet, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &replaySet{file: filepath.Join(dir, setFile)}, nil
}

func (l *replaySet) addCall(c call) error {
	return l.append(line{Kind: callLine, Call: &c})
}

func (l *replaySet) addOverride(id string, v Verdict) error {
	calls, err := l.read()
	if err != nil {
		return err
	}
	found := false
	for _, c := range calls {
		found = found || c.ID == id
	}
	if !found {
		return ErrNoCall
	}
	return l.append(line{Kind: overrideLine, Overrides: id, Override: v, At: time.Now().UTC()})
}

func (l *replaySet) append(ln line) error {
	b, err := json.Marshal(ln)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close() // the write error is the one to report
		return err
	}
	return f.Close()
}

// read returns the calls in order, each with its latest override.
func (l *replaySet) read() ([]call, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing

	var calls []call
	index := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024) // Agent messages can be long
	for sc.Scan() {
		var ln line
		if err := json.Unmarshal(sc.Bytes(), &ln); err != nil {
			// A line cut short (fh stopped mid-write) costs one
			// measurement; the rest of the set still counts.
			continue
		}
		switch ln.Kind {
		case callLine:
			if ln.Call != nil {
				index[ln.Call.ID] = len(calls)
				calls = append(calls, *ln.Call)
			}
		case overrideLine:
			if i, ok := index[ln.Overrides]; ok {
				calls[i].Override = ln.Override
			}
		}
	}
	return calls, sc.Err()
}
