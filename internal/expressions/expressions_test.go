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
	want := []any{1, 2, 3}
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
	got, err := Evaluate("${ upper(fromBase64(toBase64(\"hello\"))) }", nil, "package.toml", "value")
	if err != nil {
		t.Fatal(err)
	}
	if got != "HELLO" {
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
		if err == nil || !strings.Contains(err.Error(), "unknown name") {
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
		{name: "syntax", value: "${ 1 + }", cause: "unexpected"},
		{name: "undefined", value: "${ missing }", cause: "unknown name"},
		{name: "wrong function argument type", value: "${ upper(answer) }", env: map[string]any{"answer": 42}, cause: "cannot use int as argument"},
		{name: "function evaluation", value: "${ fromBase64(answer) }", env: map[string]any{"answer": "not base64!"}, cause: "illegal base64 data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Evaluate(tc.value, tc.env, "aact.toml", "packages.demo.inputs.answer")
			if err == nil || !strings.Contains(err.Error(), "aact.toml: packages.demo.inputs.answer:") || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("error = %v, want source path and cause %q", err, tc.cause)
			}
		})
	}
}

func TestEvaluateErrorsIncludeSourceAndPathWithoutValues(t *testing.T) {
	secret := "never-print-this-secret"
	_, err := Evaluate("${ secret_value + 1 }", map[string]any{"secret_value": secret}, "/packs/acme/profile.toml", "inputs.token")
	if err == nil {
		t.Fatal("undefined expression unexpectedly succeeded")
	}
	message := err.Error()
	for _, expected := range []string{"/packs/acme/profile.toml", "inputs.token"} {
		if !strings.Contains(message, expected) {
			t.Errorf("error %q does not include %q", message, expected)
		}
	}
	if strings.Contains(message, secret) {
		t.Fatalf("error exposed a value: %q", message)
	}
}
