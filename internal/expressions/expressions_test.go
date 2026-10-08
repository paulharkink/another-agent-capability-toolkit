package expressions

import (
	"reflect"
	"strings"
	"testing"
)

func TestEvaluatePreservesExactExpressionType(t *testing.T) {
	got, err := Evaluate("${ enabled }", map[string]any{"enabled": false}, "profile.toml", "inputs.enabled")
	if err != nil {
		t.Fatal(err)
	}
	if got != false {
		t.Fatalf("got %#v (%T), want bool false", got, got)
	}
}

func TestEvaluatePreservesExactArrayType(t *testing.T) {
	got, err := Evaluate("${ [1, 2, 3] }", nil, "package.toml", "inputs.ports")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{int64(1), int64(2), int64(3)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v (%T), want %#v", got, got, want)
	}
}

func TestEvaluatePreservesHCLObjectType(t *testing.T) {
	got, err := Evaluate(`${ { enabled = enabled, ports = [1, 2] } }`, map[string]any{"enabled": true}, "package.toml", "inputs.options")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"enabled": true, "ports": []any{int64(1), int64(2)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v (%T), want %#v", got, got, want)
	}
}

func TestEvaluateMixedInterpolationReturnsString(t *testing.T) {
	got, err := Evaluate("service-${ port }", map[string]any{"port": 8080}, "package.toml", "mcp.name")
	if err != nil {
		t.Fatal(err)
	}
	if got != "service-8080" {
		t.Fatalf("got %#v, want service-8080", got)
	}
}

func TestEvaluateTreeTraversesTablesAndArrays(t *testing.T) {
	input := map[string]any{"servers": []any{map[string]any{"url": "${ host }"}, "prefix-${ port }"}}
	env := map[string]any{"host": "https://example.test", "port": 8080}
	got, err := EvaluateTree(input, env, "pack.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"servers": []any{map[string]any{"url": "https://example.test"}, "prefix-8080"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestEvaluateEscapesExpressionDelimiter(t *testing.T) {
	got, err := Evaluate("literal $${ name }", map[string]any{"name": "ignored"}, "package.toml", "value")
	if err != nil {
		t.Fatal(err)
	}
	if got != "literal ${ name }" {
		t.Fatalf("got %#v", got)
	}
}

func TestEvaluateAllowsCuratedFunctionsAndBase64RoundTrip(t *testing.T) {
	got, err := Evaluate(`${ upper(base64decode(base64encode("hello"))) }`, nil, "package.toml", "value")
	if err != nil {
		t.Fatal(err)
	}
	if got != "HELLO" {
		t.Fatalf("got %#v", got)
	}
}

func TestEvaluateUsesFinalResolvedInputsForHCLExpressions(t *testing.T) {
	inputs := map[string]any{"username": "ada", "token": "secret-value", "enabled": false}
	got, err := Evaluate(`${ base64encode("${inputs.username}:${inputs.token}") }`, map[string]any{"inputs": inputs}, "package.toml", "mcp.token_header")
	if err != nil {
		t.Fatal(err)
	}
	if got != "YWRhOnNlY3JldC12YWx1ZQ==" {
		t.Fatalf("got %#v", got)
	}
}

func TestEvaluateStringLiteralMayContainClosingDelimiter(t *testing.T) {
	got, err := Evaluate("${ \"}\" }", nil, "package.toml", "value")
	if err != nil {
		t.Fatal(err)
	}
	if got != "}" {
		t.Fatalf("got %#v", got)
	}
}

func TestEvaluateRejectsUnavailableFunctions(t *testing.T) {
	for _, expression := range []string{"${ now() }", "${ unknownFunction(\"x\") }"} {
		_, err := Evaluate(expression, nil, "profile.toml", "inputs.value")
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unknown function") {
			t.Errorf("Evaluate(%q) error = %v, want a clear unknown-name cause", expression, err)
		}
	}
}

func TestEvaluateErrorKindsIncludeFileAndPath(t *testing.T) {
	cases := []struct {
		name  string
		value string
		env   map[string]any
		cause string
	}{
		{name: "syntax", value: "${ 1 + }", cause: "expression"},
		{name: "undefined", value: "${ missing }", cause: "unknown variable"},
		{name: "wrong function argument type", value: `${ upper(answer) }`, env: map[string]any{"answer": []any{42}}, cause: "string"},
		{name: "function evaluation", value: `${ base64decode(answer) }`, env: map[string]any{"answer": "not base64!"}, cause: "base64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Evaluate(tc.value, tc.env, "aact.toml", "packages.demo.inputs.answer")
			if err == nil || !strings.Contains(err.Error(), "aact.toml: packages.demo.inputs.answer:") || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.cause)) {
				t.Fatalf("error = %v, want source path and cause %q", err, tc.cause)
			}
		})
	}
}
