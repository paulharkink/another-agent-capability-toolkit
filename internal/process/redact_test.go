package process

import (
	"strings"
	"testing"
)

func TestStreamingRedactionAcrossChunks(t *testing.T) {
	var output strings.Builder
	r := NewRedactor([]string{"private-secret", "short"}, func(b []byte) { output.Write(b) })
	for _, s := range []string{"before pri", "vate-", "secret after sh", "ort end"} {
		r.Write([]byte(s))
	}
	r.Flush()
	if got := output.String(); got != "before [redacted] after [redacted] end" {
		t.Fatal(got)
	}
}
func TestRedactionWithoutSecretsDoesNotBuffer(t *testing.T) {
	var output string
	r := NewRedactor(nil, func(b []byte) { output += string(b) })
	r.Write([]byte("progress"))
	if output != "progress" {
		t.Fatal(output)
	}
}
