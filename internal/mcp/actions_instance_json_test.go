package mcp

import (
	"encoding/json"
	"testing"
)

func TestActionRequestUsesInstanceAndReadsLegacyProfile(t *testing.T) {
	legacy := []byte(`{"protocol_version":1,"action":"prepare","profile":{"ref":{"pack_id":"pack","capability_id":"inspect","name":"prod"}}}`)
	var request ActionRequest
	if err := json.Unmarshal(legacy, &request); err != nil {
		t.Fatal(err)
	}
	if request.Instance.Ref.Name != "prod" {
		t.Fatalf("legacy profile key did not populate instance: %+v", request.Instance)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["instance"]; !ok {
		t.Fatalf("canonical request omitted instance key: %s", encoded)
	}
	if _, ok := fields["profile"]; ok {
		t.Fatalf("canonical request still writes legacy profile key: %s", encoded)
	}
}
func TestActionRequestRejectsUnknownFields(t *testing.T) {
	var request ActionRequest
	if err := json.Unmarshal([]byte(`{"protocol_version":1,"surprise":true}`), &request); err == nil {
		t.Fatal("unknown action request field was accepted")
	}
}
func TestActionRequestReadsLegacyProfileCaseInsensitively(t *testing.T) {
	var request ActionRequest
	if err := json.Unmarshal([]byte(`{"Profile":{"ref":{"pack_id":"pack","capability_id":"inspect","name":"old"}}}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Instance.Ref.Name != "old" {
		t.Fatalf("case-insensitive legacy profile key did not populate instance: %+v", request.Instance)
	}
}
func TestActionRequestLegacyProfileDuplicateUsesLastSourceValue(t *testing.T) {
	input := []byte(`{"Profile":{"ref":{"name":"first"}},"profile":{"ref":{"name":"last"}}}`)
	for range 100 {
		var request ActionRequest
		if err := json.Unmarshal(input, &request); err != nil {
			t.Fatal(err)
		}
		if request.Instance.Ref.Name != "last" {
			t.Fatalf("got %q, want last", request.Instance.Ref.Name)
		}
	}
}
func TestActionRequestValidatesLegacyProfileEvenWhenInstanceIsPresent(t *testing.T) {
	var request ActionRequest
	if err := json.Unmarshal([]byte(`{"instance":{},"profile":"bad"}`), &request); err == nil {
		t.Fatal("invalid legacy profile value was ignored")
	}
}
