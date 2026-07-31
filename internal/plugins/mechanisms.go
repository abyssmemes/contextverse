package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orkcom-tech/contextverse/internal/logx"
)

// ApplyResult describes one integration apply.
type ApplyResult struct {
	ID      string
	Target  string
	Action  string // merged|wrote|skipped|manual
	Message string
}

// Apply wires one integration into its session-start slot.
func Apply(in *Integration, vars Vars) (*ApplyResult, error) {
	if in == nil {
		return nil, fmt.Errorf("nil integration")
	}
	switch in.Mechanism {
	case MechanismCommandHook:
		return applyCommandHook(in, vars)
	case MechanismRulesSlot, MechanismInstructionsSlot:
		return applySlot(in, vars)
	case MechanismManual:
		msg := ManualInstructions(in, vars)
		fmt.Fprint(os.Stderr, msg)
		return &ApplyResult{ID: in.ID, Action: "manual", Message: "printed instructions"}, nil
	default:
		return nil, fmt.Errorf("unknown mechanism %q", in.Mechanism)
	}
}

func applyCommandHook(in *Integration, vars Vars) (*ApplyResult, error) {
	if in.Command == "" {
		return nil, fmt.Errorf("%s: command required for command-hook", in.ID)
	}
	target := Expand(in.Target, vars)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if raw, err := os.ReadFile(target); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w (fix before merge)", target, err)
		}
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	session, _ := hooks["SessionStart"].([]any)
	entry := map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": in.Command,
			},
		},
	}
	// Replace existing contextd-owned hook (same command) or append.
	replaced := false
	for i, item := range session {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if fmt.Sprint(hm["command"]) == in.Command {
				session[i] = entry
				replaced = true
				break
			}
		}
		if replaced {
			break
		}
	}
	if !replaced {
		session = append(session, entry)
	}
	hooks["SessionStart"] = session

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	if err := os.WriteFile(target, out, 0o644); err != nil {
		return nil, err
	}
	action := "merged"
	if replaced {
		action = "updated"
	}
	logx.L().Info("plugin command-hook applied", "id", in.ID, "target", target, "action", action)
	return &ApplyResult{ID: in.ID, Target: target, Action: action}, nil
}

// Markers delimiting the region contextd owns inside a file it shares with the
// user. HTML comments render as nothing in Markdown and are inert in the plain
// text rules files, so they are safe across every slot target we write.
const (
	slotBlockBegin = "<!-- >>> contextverse >>> -->"
	slotBlockEnd   = "<!-- <<< contextverse <<< -->"
)

func applySlot(in *Integration, vars Vars) (*ApplyResult, error) {
	target := Expand(in.Target, vars)
	body, err := renderPayload(in, vars)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}

	// A slot target the user also writes (AGENTS.md is read by several agents and
	// usually hand-authored) must not be overwritten wholesale — contextd owns a
	// marked block inside it and leaves everything else alone.
	if in.Merge == MergeMarkedBlock {
		action, err := mergeSlotBlock(target, body)
		if err != nil {
			return nil, err
		}
		logx.L().Info("plugin slot merged", "id", in.ID, "target", target, "mechanism", in.Mechanism, "action", action)
		return &ApplyResult{ID: in.ID, Target: target, Action: action}, nil
	}

	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return nil, err
	}
	logx.L().Info("plugin slot applied", "id", in.ID, "target", target, "mechanism", in.Mechanism)
	return &ApplyResult{ID: in.ID, Target: target, Action: "wrote"}, nil
}

// mergeSlotBlock writes body between the contextd markers in target, keeping any
// content the user put around them. A file without markers gets the block
// appended; a missing file is created holding only the block.
func mergeSlotBlock(target, body string) (string, error) {
	block := slotBlockBegin + "\n" + strings.TrimRight(body, "\n") + "\n" + slotBlockEnd + "\n"

	raw, err := os.ReadFile(target)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		return "wrote", os.WriteFile(target, []byte(block), 0o644)
	}

	existing := string(raw)
	i := strings.Index(existing, slotBlockBegin)
	if i < 0 {
		out := existing
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if out != "" {
			out += "\n"
		}
		return "merged", os.WriteFile(target, []byte(out+block), 0o644)
	}

	jRel := strings.Index(existing[i:], slotBlockEnd)
	if jRel < 0 {
		// Half a block means someone edited inside our region; refusing is safer
		// than guessing where it ended and eating their text.
		return "", fmt.Errorf("%s: contextverse begin marker without end — fix or remove the block, then re-run", target)
	}
	j := i + jRel + len(slotBlockEnd)
	for j < len(existing) && (existing[j] == '\n' || existing[j] == '\r') {
		j++
	}
	out := existing[:i] + block + existing[j:]
	return "merged", os.WriteFile(target, []byte(out), 0o644)
}

func renderPayload(in *Integration, vars Vars) (string, error) {
	name := in.Payload
	if name == "" {
		name = "payload.tmpl"
	}
	path := filepath.Join(in.Dir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: payload: %w", in.ID, err)
	}
	body := Expand(string(raw), vars)
	if vars.Project != "" && !strings.Contains(body, vars.Project) {
		// optional project line for templates that include {{project}}
		_ = vars.Project
	}
	return body, nil
}

// ManualInstructions returns copy-paste setup text for a client (or generic).
func ManualInstructions(in *Integration, vars Vars) string {
	var b strings.Builder
	if in == nil {
		return ManualInstructionsCatalog(nil, vars)
	}
	fmt.Fprintf(&b, "Manual setup for %s (%s):\n", in.Display, in.ID)
	fmt.Fprintf(&b, "  mechanism: %s\n", in.Mechanism)
	if in.Mechanism == MechanismCommandHook {
		fmt.Fprintf(&b, "  target:    %s\n", Expand(in.Target, vars))
		fmt.Fprintf(&b, "  command:   %s\n", in.Command)
	} else if in.Target != "" {
		fmt.Fprintf(&b, "  write:     %s\n", Expand(in.Target, vars))
	}
	if in.Notes != "" {
		fmt.Fprintf(&b, "  notes:     %s\n", in.Notes)
	}
	if strings.TrimSpace(in.Manual) != "" {
		b.WriteString("\n")
		b.WriteString(Expand(strings.TrimSpace(in.Manual), vars))
		b.WriteString("\n")
	}
	return b.String()
}

// ManualInstructionsCatalog prints fallback for every known integration.
func ManualInstructionsCatalog(catalog []*Integration, vars Vars) string {
	var b strings.Builder
	b.WriteString("No supported AI client detected on this machine.\n")
	b.WriteString("Configure a session-start slot manually, or run:\n")
	b.WriteString("  contextd plugin install <id>\n")
	b.WriteString("  contextd plugin list\n\n")
	if len(catalog) == 0 {
		b.WriteString("For Claude Code, add to ~/.claude/settings.json:\n\n")
		b.WriteString("  \"hooks\": { \"SessionStart\": [ { \"hooks\": [\n")
		b.WriteString("    { \"type\": \"command\", \"command\": \"contextd context inject --format claude-hook\" }\n")
		b.WriteString("  ] } ] }\n\n")
		b.WriteString("Templates: https://github.com/orkcom-tech/contextverse-templates\n")
		return b.String()
	}
	for _, in := range catalog {
		b.WriteString("---\n")
		b.WriteString(ManualInstructions(in, vars))
		b.WriteString("\n")
	}
	b.WriteString("Contribute a client-integration template:\n")
	b.WriteString("  https://github.com/orkcom-tech/contextverse-templates\n")
	return b.String()
}
