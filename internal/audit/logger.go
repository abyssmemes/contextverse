package audit

import (
	"bufio"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Result values for Entry.Result.
const (
	ResultSuccess = "success"
	ResultDenied  = "denied"
	ResultError   = "error"
)

// Actor is who performed the action.
type Actor struct {
	Username string `json:"username"`
	Role     string `json:"role,omitempty"`
	IP       string `json:"ip,omitempty"`
	Method   string `json:"method,omitempty"` // token|userpass|session|local
}

// Diff is optional write metadata.
type Diff struct {
	LinesAdded   int    `json:"lines_added,omitempty"`
	LinesRemoved int    `json:"lines_removed,omitempty"`
	ContentHash  string `json:"content_hash,omitempty"`
	Ops          int    `json:"ops,omitempty"`
}

// Entry is one immutable audit record (JSONL line).
type Entry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Actor     Actor     `json:"actor"`
	Action    string    `json:"action"`
	Space     string    `json:"space,omitempty"`
	Target    string    `json:"target,omitempty"`
	Diff      *Diff     `json:"diff,omitempty"`
	Result    string    `json:"result"`
	Error     string    `json:"error,omitempty"`
}

// Filter selects entries for Query / Export.
type Filter struct {
	Since  time.Time
	Until  time.Time
	Actor  string // exact username
	Action string // substring or glob-ish *action*
	Space  string
	Result string
	Limit  int // 0 = default 200; -1 = no limit
}

// Stats summarizes a window.
type Stats struct {
	Entries      int            `json:"entries"`
	Actors       int            `json:"actors"`
	Failed       int            `json:"failed"`
	ByAction     map[string]int `json:"by_action"`
	Since        time.Time      `json:"since"`
	Until        time.Time      `json:"until"`
}

// Logger appends JSONL under <dir>/YYYY-MM-DD.jsonl.
type Logger struct {
	mu  sync.Mutex
	dir string
}

// Open creates (or reuses) an audit directory under dataDir/audit.
func Open(dataDir string) (*Logger, error) {
	dir := filepath.Join(dataDir, "audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Logger{dir: dir}, nil
}

// Dir returns the audit directory.
func (l *Logger) Dir() string { return l.dir }

// Append writes one entry (fills id/timestamp if empty). Never deletes.
func (l *Logger) Append(e Entry) error {
	if l == nil {
		return nil
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	} else {
		e.Timestamp = e.Timestamp.UTC()
	}
	if e.ID == "" {
		e.ID = newID(e.Timestamp)
	}
	if e.Result == "" {
		e.Result = ResultSuccess
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	day := e.Timestamp.Format("2006-01-02")
	path := filepath.Join(l.dir, day+".jsonl")

	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func newID(ts time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("evt_%s%s", ts.Format("20060102T150405"), hex.EncodeToString(b[:]))
}

// Query scans day files matching the filter (newest first).
func (l *Logger) Query(f Filter) ([]Entry, error) {
	if l == nil {
		return nil, nil
	}
	limit := f.Limit
	if limit == 0 {
		limit = 200
	}
	unlimited := limit < 0
	files, err := l.dayFiles()
	if err != nil {
		return nil, err
	}
	// newest first
	sort.Slice(files, func(i, j int) bool { return files[i] > files[j] })

	var out []Entry
	for _, name := range files {
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(name, ".jsonl"), time.UTC)
		if err != nil {
			continue
		}
		if !f.Until.IsZero() && day.After(f.Until) {
			continue
		}
		if !f.Since.IsZero() && day.Add(24*time.Hour).Before(f.Since) {
			continue
		}
		path := filepath.Join(l.dir, name)
		entries, err := readFile(path)
		if err != nil {
			return out, err
		}
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if !match(e, f) {
				continue
			}
			out = append(out, e)
			if !unlimited && len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// Stats aggregates matching entries (no limit by default).
func (l *Logger) Stats(f Filter) (Stats, error) {
	f.Limit = -1
	entries, err := l.Query(f)
	if err != nil {
		return Stats{}, err
	}
	st := Stats{
		ByAction: map[string]int{},
		Since:    f.Since,
		Until:    f.Until,
	}
	actors := map[string]struct{}{}
	for _, e := range entries {
		st.Entries++
		actors[e.Actor.Username] = struct{}{}
		st.ByAction[e.Action]++
		if e.Result != ResultSuccess {
			st.Failed++
		}
	}
	st.Actors = len(actors)
	return st, nil
}

// ExportJSONL writes matching entries as JSONL to w.
func (l *Logger) ExportJSONL(w io.Writer, f Filter) error {
	f.Limit = -1
	entries, err := l.Query(f)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

// ExportCSV writes matching entries as CSV to w.
func (l *Logger) ExportCSV(w io.Writer, f Filter) error {
	f.Limit = -1
	entries, err := l.Query(f)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "timestamp", "actor", "role", "ip", "action", "space", "target", "result", "error"})
	for _, e := range entries {
		_ = cw.Write([]string{
			e.ID,
			e.Timestamp.UTC().Format(time.RFC3339),
			e.Actor.Username,
			e.Actor.Role,
			e.Actor.IP,
			e.Action,
			e.Space,
			e.Target,
			e.Result,
			e.Error,
		})
	}
	cw.Flush()
	return cw.Error()
}

func (l *Logger) dayFiles() ([]string, error) {
	ents, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

func readFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

func match(e Entry, f Filter) bool {
	if !f.Since.IsZero() && e.Timestamp.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && e.Timestamp.After(f.Until) {
		return false
	}
	if f.Actor != "" && !strings.EqualFold(e.Actor.Username, f.Actor) {
		return false
	}
	if f.Space != "" && e.Space != f.Space {
		return false
	}
	if f.Result != "" && e.Result != f.Result {
		return false
	}
	if f.Action != "" && !matchAction(e.Action, f.Action) {
		return false
	}
	return true
}

func matchAction(got, pat string) bool {
	pat = strings.TrimSpace(pat)
	if pat == "" || pat == "*" {
		return true
	}
	if strings.Contains(pat, "*") {
		// simple *foo* / foo* / *foo
		parts := strings.Split(pat, "*")
		rest := got
		for i, p := range parts {
			if p == "" {
				continue
			}
			idx := strings.Index(rest, p)
			if idx < 0 {
				return false
			}
			if i == 0 && !strings.HasPrefix(pat, "*") && idx != 0 {
				return false
			}
			rest = rest[idx+len(p):]
		}
		if !strings.HasSuffix(pat, "*") && rest != "" && parts[len(parts)-1] != "" {
			return false
		}
		return true
	}
	return strings.Contains(got, pat)
}

// ParseSince parses durations like 24h / 7d or RFC3339 / YYYY-MM-DD.
func ParseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.UTC); err == nil {
		return t.UTC(), nil
	}
	// 24h, 7d, 90d
	if strings.HasSuffix(s, "d") {
		var n int
		if _, err := fmt.Sscanf(s, "%dd", &n); err == nil {
			return time.Now().UTC().Add(-time.Duration(n) * 24 * time.Hour), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --since %q (use 24h, 7d, RFC3339, or YYYY-MM-DD)", s)
	}
	return time.Now().UTC().Add(-d), nil
}
