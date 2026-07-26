// Package cliout renders command results in the format the caller asked for.
//
// The design constraint is that a command must not have two implementations of
// its own output. Each one builds a typed value and supplies a human renderer;
// this package decides whether to print that rendering or marshal the value.
// The structured payload is therefore always the same data the human sees, and
// adding a field cannot leave `--json` behind.
package cliout

import (
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Format is the output encoding.
type Format string

const (
	Human Format = "human"
	JSON  Format = "json"
	YAML  Format = "yaml"
)

// Pick resolves the mutually exclusive --json / --yaml flags.
func Pick(jsonFlag, yamlFlag bool) (Format, error) {
	switch {
	case jsonFlag && yamlFlag:
		return "", fmt.Errorf("--json and --yaml are mutually exclusive")
	case jsonFlag:
		return JSON, nil
	case yamlFlag:
		return YAML, nil
	default:
		return Human, nil
	}
}

// Emit writes v in f, falling back to the human renderer for Human.
//
// human may be nil for commands whose structured form is the only sensible one;
// v may be nil for commands that only have prose.
func Emit(w io.Writer, f Format, v any, human func(io.Writer) error) error {
	switch f {
	case JSON:
		if v == nil {
			return nil
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case YAML:
		if v == nil {
			return nil
		}
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(v); err != nil {
			return err
		}
		return enc.Close()
	default:
		if human == nil {
			return nil
		}
		return human(w)
	}
}

// Structured reports whether f is machine-readable. Commands use it to suppress
// progress chatter that would corrupt a JSON stream.
func (f Format) Structured() bool { return f == JSON || f == YAML }
