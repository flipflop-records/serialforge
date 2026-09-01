package main

import "testing"

func testDefs() []flagDef {
	return []flagDef{
		{names: []string{"--port", "--path"}, takesValue: true},
		{names: []string{"--baud"}, takesValue: true},
		{names: []string{"--hex"}, takesValue: false},
		{names: []string{"--text"}, takesValue: false},
		{names: []string{"--ascii"}, takesValue: false},
		{names: []string{"--both"}, takesValue: false},
	}
}

func TestParseArgsOrderIndependent(t *testing.T) {
	cases := [][]string{
		{"--port", "/tmp/a", "--baud", "115200", "--hex"},
		{"--hex", "--baud", "115200", "--port", "/tmp/a"},
		{"--baud", "115200", "--port", "/tmp/a", "--hex"},
		{"--hex", "--port", "/tmp/a", "--baud", "115200"},
	}
	for _, args := range cases {
		got, err := parseArgs(args, testDefs())
		if err != nil {
			t.Fatalf("parseArgs(%v): %v", args, err)
		}
		port, ok, err := got.single("--port", "--port/--path")
		if err != nil || !ok || port != "/tmp/a" {
			t.Errorf("parseArgs(%v): port = %q, %v, %v; want /tmp/a, true, nil", args, port, ok, err)
		}
		baud, ok, err := got.single("--baud", "--baud")
		if err != nil || !ok || baud != "115200" {
			t.Errorf("parseArgs(%v): baud = %q, %v, %v; want 115200, true, nil", args, baud, ok, err)
		}
		if !got.has("--hex") {
			t.Errorf("parseArgs(%v): --hex not recorded as present", args)
		}
	}
}

func TestParseArgsAliasesShareCanonicalKey(t *testing.T) {
	got, err := parseArgs([]string{"--path", "/tmp/a"}, testDefs())
	if err != nil {
		t.Fatal(err)
	}
	v, ok, err := got.single("--port", "--port/--path")
	if err != nil || !ok || v != "/tmp/a" {
		t.Fatalf("--path should be filed under the canonical --port key: got %q, %v, %v", v, ok, err)
	}
}

func TestParseArgsPositionals(t *testing.T) {
	got, err := parseArgs([]string{"/tmp/a", "AA 55", "--hex"}, testDefs())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/tmp/a", "AA 55"}
	if len(got.positionals) != len(want) || got.positionals[0] != want[0] || got.positionals[1] != want[1] {
		t.Fatalf("positionals = %v, want %v", got.positionals, want)
	}
}

func TestParseArgsUnknownFlagIsError(t *testing.T) {
	_, err := parseArgs([]string{"--bogus", "x"}, testDefs())
	if err == nil {
		t.Fatal("parseArgs with an unrecognized flag should error, not silently treat it as positional")
	}
}

func TestParseArgsMissingValueIsError(t *testing.T) {
	_, err := parseArgs([]string{"--port"}, testDefs())
	if err == nil {
		t.Fatal("a value-taking flag with nothing following it should error")
	}
}

func TestParseArgsBooleanFlagRejectsInlineValue(t *testing.T) {
	_, err := parseArgs([]string{"--hex=foo"}, testDefs())
	if err == nil {
		t.Fatal("a boolean flag given as --flag=value should error")
	}
}

func TestParseArgsInlineEqualsForm(t *testing.T) {
	got, err := parseArgs([]string{"--port=/tmp/a"}, testDefs())
	if err != nil {
		t.Fatal(err)
	}
	v, ok, _ := got.single("--port", "--port/--path")
	if !ok || v != "/tmp/a" {
		t.Fatalf("--port=/tmp/a: got %q, %v", v, ok)
	}
}

func TestParsedArgsSingleDetectsConflict(t *testing.T) {
	got, err := parseArgs([]string{"--port", "/tmp/a", "--path", "/tmp/b"}, testDefs())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = got.single("--port", "--port/--path")
	if err == nil {
		t.Fatal("--port and --path given with different values should be a conflict error")
	}
}

func TestParsedArgsSingleAllowsRedundantSameValue(t *testing.T) {
	got, err := parseArgs([]string{"--port", "/tmp/a", "--path", "/tmp/a"}, testDefs())
	if err != nil {
		t.Fatal(err)
	}
	v, ok, err := got.single("--port", "--port/--path")
	if err != nil || !ok || v != "/tmp/a" {
		t.Fatalf("identical --port/--path values should not conflict: got %q, %v, %v", v, ok, err)
	}
}
