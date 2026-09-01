package main

import (
	"fmt"
	"strings"
)

// This file is the CLI's one argument-parsing layer, used by every
// subcommand that mixes flags with a device/payload (monitor, send, batch
// run). It exists because the previous ad hoc scanning conflated "the
// first token" with "the device," so `serialforge monitor --port
// /tmp/serialforge-a --baud 115200` tried to open a port literally named
// "--port" — flags anywhere in argv must always parse as flags,
// independent of position; see ARCHITECTURE.md's CLI argument-parsing invariant.

// flagDef declares one recognized flag: every name it can be spelled as
// (names[0] is canonical — what results are filed under) and whether it
// consumes the following token as a value.
type flagDef struct {
	names      []string
	takesValue bool
}

// parsedArgs is the result of parseArgs: every recognized flag's values
// (in the order given, filed under its canonical name — more than one
// entry means the flag, or one of its aliases, was given more than once),
// and every non-flag token, in order, as positionals.
type parsedArgs struct {
	values      map[string][]string
	positionals []string
}

func (p parsedArgs) has(canonical string) bool { return len(p.values[canonical]) > 0 }

// single returns canonical's one value, erroring if it was given more than
// once with conflicting values (repeating the same value, e.g. via two
// aliases for the same flag, is not an error). label is the flag's
// user-facing name(s) for the error message, e.g. "--port/--path".
func (p parsedArgs) single(canonical, label string) (string, bool, error) {
	vs := p.values[canonical]
	if len(vs) == 0 {
		return "", false, nil
	}
	for _, v := range vs[1:] {
		if v != vs[0] {
			return "", false, fmt.Errorf("conflicting %s values: %q vs %q", label, vs[0], v)
		}
	}
	return vs[0], true, nil
}

// parseArgs scans args against defs, order-independent: a recognized flag
// (by any alias) is consumed wherever it appears in the list; a token that
// doesn't start with "-" (and isn't a value already claimed by a preceding
// value-taking flag) becomes a positional; an unrecognized "-"-prefixed
// token is a hard error, never silently treated as a positional — that is
// exactly the bug this replaces (a flag typo or a not-yet-supported flag
// must never be mistaken for a device path).
func parseArgs(args []string, defs []flagDef) (parsedArgs, error) {
	lookup := make(map[string]*flagDef, len(defs)*2)
	for i := range defs {
		for _, n := range defs[i].names {
			lookup[n] = &defs[i]
		}
	}
	result := parsedArgs{values: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "" || a[0] != '-' || a == "-" {
			result.positionals = append(result.positionals, a)
			continue
		}
		name, inlineValue, hasInline := a, "", false
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			name, inlineValue, hasInline = a[:eq], a[eq+1:], true
		}
		def, ok := lookup[name]
		if !ok {
			return result, fmt.Errorf("unknown flag %q", name)
		}
		canonical := def.names[0]
		if !def.takesValue {
			if hasInline {
				return result, fmt.Errorf("flag %q does not take a value", name)
			}
			result.values[canonical] = append(result.values[canonical], "")
			continue
		}
		if hasInline {
			result.values[canonical] = append(result.values[canonical], inlineValue)
			continue
		}
		if i+1 >= len(args) {
			return result, fmt.Errorf("flag %q requires a value", name)
		}
		i++
		result.values[canonical] = append(result.values[canonical], args[i])
	}
	return result, nil
}

// wantsHelp does a cheap pre-scan for -h/--help without needing a full
// parseArgs pass first — subcommands check this before parsing so an
// otherwise-invalid/incomplete command (missing device, bad flags) still
// shows help instead of an error when --help was the actual intent.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}
