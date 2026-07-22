package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abyssmemes/contextverse/internal/logx"
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

func applySlot(in *Integration, vars Vars) (*ApplyResult, error) {
	target := Expand(in.Target, vars)
	body, err := renderPayload(in, vars)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return nil, err
	}
	logx.L().Info("plugin slot applied", "id", in.ID, "target", target, "mechanism", in.Mechanism)
	return &ApplyResult{ID: in.ID, Target: target, Action: "wrote"}, nil
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
		b.WriteString("No supported AI client detected on this machine.\n")
		b.WriteString("To deliver ContextVerse context at session start, configure your AI's\n")
		b.WriteString("session-start slot manually. For Claude Code, add to ~/.claude/settings.json:\n\n")
		b.WriteString("  \"hooks\": { \"SessionStart\": [ { \"hooks\": [\n")
		b.WriteString("    { \"type\": \"command\", \"command\": \"contextd context inject --format claude-hook\" }\n")
		b.WriteString("  ] } ] }\n\n")
		b.WriteString("Using a different AI? See `contextd context inject --list` for known formats,\n")
		b.WriteString("or add a client-integration template: https://github.com/abyssmemes/contextverse-templates\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("Manual setup for %s (%s):\n", in.Display, in.ID))
	b.WriteString(fmt.Sprintf("  mechanism: %s\n", in.Mechanism))
	if in.Mechanism == MechanismCommandHook {
		b.WriteString(fmt.Sprintf("  target:    %s\n", Expand(in.Target, vars)))
		b.WriteString(fmt.Sprintf("  command:   %s\n", in.Command))
	} else if in.Target != "" {
		b.WriteString(fmt.Sprintf("  write:     %s\n", Expand(in.Target, vars)))
	}
	if in.Notes != "" {
		b.WriteString(fmt.Sprintf("  notes:     %s\n", in.Notes))
	}
	return b.String()
}
