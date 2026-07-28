// Package bench measures eager against lazy context delivery.
//
// # The question
//
// The product hands an AI its whole entry set at the start of every session.
// Whether that beats letting the client fetch what it decides it needs has
// never been measured, so no token-efficiency claim appears anywhere in the
// user-facing copy. This package is how that claim gets earned or dropped.
//
// # What this measures, and what it does not
//
// Answer quality needs a model, an API key and money, so it lives behind an
// explicit flag and is not part of the suite. What runs here is deterministic
// and free, and it settles the prior question:
//
//   - is the answer even reachable in this arm, and
//   - what does reaching it cost.
//
// That is a necessary condition, not a sufficient one. An arm that cannot reach
// the answer cannot answer correctly, whatever the model does — and an entry
// set that omits the answering document is expensive *and* wrong, which is a
// finding no model run is needed to establish. An arm that does reach it still
// has to be shown to help, and that part is honestly still open.
//
// Reported either way. If lazy wins, the session-start payload should shrink to
// a routing pointer, and that is a different product rather than a tuning
// change. Running a benchmark you are only willing to believe in one direction
// is not measuring, it is decorating.
package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/abyssmemes/contextverse/internal/graph"
	"github.com/abyssmemes/contextverse/internal/search"
)

// Task is one question with the documents that actually answer it.
type Task struct {
	// Question is what someone would ask their assistant.
	Question string `yaml:"question" json:"question"`
	// Answers lists the documents containing the answer. More than one is
	// allowed: reaching any of them counts, because a question answered from
	// either of two documents is answered.
	Answers []string `yaml:"answers" json:"answers"`
}

// TaskSet is a benchmark's questions, kept beside the space they describe.
type TaskSet struct {
	Space string `yaml:"space" json:"space"`
	Tasks []Task `yaml:"tasks" json:"tasks"`
}

// LoadTasks reads a task set from disk.
func LoadTasks(path string) (*TaskSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ts TaskSet
	if err := yaml.Unmarshal(b, &ts); err != nil {
		return nil, fmt.Errorf("parse task set %s: %w", path, err)
	}
	if len(ts.Tasks) == 0 {
		return nil, fmt.Errorf("%s defines no tasks", path)
	}
	for i, t := range ts.Tasks {
		if len(t.Answers) == 0 {
			return nil, fmt.Errorf("task %d (%q) names no answering document", i+1, t.Question)
		}
	}
	return &ts, nil
}

// Arm is one delivery strategy.
type Arm string

const (
	// ArmNothing is the baseline: the model gets no context at all. It exists
	// to show what the other arms are actually buying.
	ArmNothing Arm = "nothing"
	// ArmEntrySet is today's behaviour: the whole entry set, every session.
	ArmEntrySet Arm = "entry-set"
	// ArmMap is a token-budgeted graph map plus the tools to fetch on demand.
	ArmMap Arm = "map"
)

// TaskResult is one question under one arm.
type TaskResult struct {
	Question string `json:"question" yaml:"question"`
	// Reached reports whether a document that answers the question ended up in
	// front of the model.
	Reached bool `json:"reached" yaml:"reached"`
	// Via names how: "entry-set" if it was sent up front, "search"/"neighbors"
	// if the retrieval had to go and get it.
	Via string `json:"via,omitempty" yaml:"via,omitempty"`
	// UpfrontTokens is what the session paid before the question was asked.
	UpfrontTokens int `json:"upfront_tokens" yaml:"upfront_tokens"`
	// FetchedTokens is what answering this question cost on top.
	FetchedTokens int `json:"fetched_tokens" yaml:"fetched_tokens"`
	// Steps is how many retrieval calls it took. Zero for eager delivery, which
	// is the whole of its appeal.
	Steps int `json:"steps" yaml:"steps"`
}

// Total is what the question cost in all.
func (r TaskResult) Total() int { return r.UpfrontTokens + r.FetchedTokens }

// ArmResult is one arm over the whole task set.
type ArmResult struct {
	Arm     Arm          `json:"arm" yaml:"arm"`
	Reached int          `json:"reached" yaml:"reached"`
	Total   int          `json:"total" yaml:"total"`
	Upfront int          `json:"upfront_tokens" yaml:"upfront_tokens"`
	Fetched int          `json:"fetched_tokens" yaml:"fetched_tokens"`
	Tasks   []TaskResult `json:"tasks" yaml:"tasks"`
}

// TokensPerAnswer is the number that matters: paying less to reach nothing is
// not a win. Zero reached means the cost bought nothing, and dividing by zero
// to make that look good is exactly the arithmetic to avoid.
func (a ArmResult) TokensPerAnswer() int {
	if a.Reached == 0 {
		return 0
	}
	return (a.Upfront + a.Fetched) / a.Reached
}

// Report is the whole run.
type Report struct {
	Space  string      `json:"space" yaml:"space"`
	Tasks  int         `json:"tasks" yaml:"tasks"`
	Budget int         `json:"map_budget" yaml:"map_budget"`
	Arms   []ArmResult `json:"arms" yaml:"arms"`
}

// approxTokens matches the estimate the rest of the tool uses: roughly four
// characters per token. Crude, but consistent — and the comparison between arms
// is what matters here, not the absolute figure. Swapping in a real tokeniser
// would move every arm by about the same factor.
func approxTokens(s string) int { return len(s) / 4 }

// Options configures a run.
type Options struct {
	Root string
	// Budget is the token allowance for the map arm.
	Budget int
	// MaxSteps caps retrieval. A policy allowed unlimited fetches would
	// eventually read the whole space and "reach" everything, which measures
	// nothing.
	MaxSteps int
}

// Run measures every arm over the task set.
func Run(ctx context.Context, ts *TaskSet, opts Options) (*Report, error) {
	if opts.Budget <= 0 {
		opts.Budget = 700
	}
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 3
	}

	g, err := graph.Load(graph.Options{Root: opts.Root})
	if err != nil {
		return nil, fmt.Errorf("load graph: %w", err)
	}

	rep := &Report{Space: opts.Root, Tasks: len(ts.Tasks), Budget: opts.Budget}
	for _, arm := range []Arm{ArmNothing, ArmEntrySet, ArmMap} {
		res, err := runArm(ctx, arm, ts, g, opts)
		if err != nil {
			return nil, err
		}
		rep.Arms = append(rep.Arms, res)
	}
	return rep, nil
}

func runArm(ctx context.Context, arm Arm, ts *TaskSet, g *graph.Graph, opts Options) (ArmResult, error) {
	res := ArmResult{Arm: arm, Total: len(ts.Tasks)}

	// The session-start payload is paid once, not per question. Charging it to
	// every task would flatter lazy delivery by multiplying its opponent's bill
	// by the size of the task set.
	var upfront int
	var delivered map[string]bool

	switch arm {
	case ArmNothing:
		delivered = map[string]bool{}
	case ArmEntrySet:
		docs, err := entrySet(opts.Root)
		if err != nil {
			return res, err
		}
		delivered = map[string]bool{}
		for path, body := range docs {
			upfront += approxTokens(body)
			delivered[path] = true
		}
	case ArmMap:
		m := graph.RenderMap(g, graph.MapOptions{Budget: opts.Budget})
		upfront = approxTokens(m)
		delivered = map[string]bool{}
	}
	res.Upfront = upfront

	for _, t := range ts.Tasks {
		tr := TaskResult{Question: t.Question}
		// Attribute the shared payload to the first task only, so the arm total
		// is right and per-task figures stay honest about what is shared.
		if len(res.Tasks) == 0 {
			tr.UpfrontTokens = upfront
		}

		if hit := firstDelivered(t.Answers, delivered); hit != "" {
			tr.Reached, tr.Via = true, string(arm)
		} else if arm == ArmMap {
			fetched, steps, via, hit := retrieve(ctx, t, g, opts)
			tr.FetchedTokens, tr.Steps, tr.Via = fetched, steps, via
			tr.Reached = hit
			res.Fetched += fetched
		}

		if tr.Reached {
			res.Reached++
		}
		res.Tasks = append(res.Tasks, tr)
	}
	return res, nil
}

func firstDelivered(answers []string, delivered map[string]bool) string {
	for _, a := range answers {
		if delivered[a] {
			return a
		}
	}
	return ""
}

// retrieve models what a client with the map and the tools would plausibly do:
// search for the question's distinctive words, then walk the graph outward from
// the best hit.
//
// This is a floor, not a ceiling. A real model would phrase better queries than
// "strip the stop words and search"; if even this policy reaches the answer,
// the tools are sufficient. If it does not, that is a question about this
// policy as much as about the design, and the report says which.
func retrieve(ctx context.Context, t Task, g *graph.Graph, opts Options) (tokens, steps int, via string, hit bool) {
	want := map[string]bool{}
	for _, a := range t.Answers {
		want[a] = true
	}

	for _, q := range queries(t.Question) {
		if steps >= opts.MaxSteps {
			break
		}
		steps++
		res, err := search.Search(search.Options{Root: opts.Root, Query: q, Limit: 5})
		if err != nil || res == nil {
			continue
		}
		for _, m := range res.Matches {
			tokens += approxTokens(m.Text)
			if want[m.Path] {
				return tokens, steps, "search", true
			}
		}
		// Nothing matched directly; the neighbours of the best hit are the next
		// thing a reader would try, and the graph is there to make that cheap.
		if len(res.Matches) > 0 && steps < opts.MaxSteps {
			steps++
			out, in := g.Neighbours(res.Matches[0].Path)
			for _, e := range append(out, in...) {
				// Both ends: a document that links to the answer and one the
				// answer links to are equally one hop away when you are reading.
				for _, p := range []string{e.From, e.To} {
					tokens += approxTokens(p)
					if want[p] {
						return tokens, steps, "neighbors", true
					}
				}
			}
		}
	}
	return tokens, steps, "", false
}

// stopWords are too common to discriminate between documents in a space this
// small; searching for "the" returns everything and locates nothing.
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "do": true, "does": true,
	"for": true, "how": true, "i": true, "in": true, "is": true, "it": true,
	"my": true, "of": true, "on": true, "our": true, "the": true, "to": true,
	"we": true, "what": true, "when": true, "where": true, "which": true,
	"who": true, "why": true, "with": true,
}

// queries turns a question into search terms, longest first: the longest word
// is usually the most specific one.
func queries(question string) []string {
	seen := map[string]bool{}
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(question), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	}) {
		if len(w) < 3 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	sort.SliceStable(words, func(i, j int) bool { return len(words[i]) > len(words[j]) })
	return words
}

// entrySet is what a session is handed today: the documents context-entry.md
// routes to, which is the payload this benchmark exists to question.
func entrySet(root string) (map[string]string, error) {
	out := map[string]string{}
	for _, rel := range []string{
		"context-entry.md",
		"identity/me.md",
		"team/principles.md",
		"space-index.md",
		"decisions.md",
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue // a space without one of these is normal, not an error
		}
		out[rel] = string(b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no entry-set documents found under %s", root)
	}
	return out, nil
}
