package app

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalProfileNameUsesPackProfileGrammar(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	for _, name := range []string{"bad:name", "a b", ".hidden", "bad\rname"} {
		q.Ref.Name = name
		if err := s.CreateProfile(context.Background(), q.Ref); err == nil {
			t.Errorf("accepted unusable local profile %q", name)
		}
	}
}

func TestProfilePreservesAbsoluteDockerBuildContext(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCPs = p.MCPs[:1]
	p.Sets[0].MCPs = []string{"alpha"}
	build := t.TempDir()
	p.MCPs[0].BuildContext = build
	runtime := &providerTestRuntime{}
	s.Options.Runtime = runtime
	if _, err := s.RunProfileMCP(context.Background(), "start", q, "alpha"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 || runtime.specs[0].BuildContext != build {
		t.Fatalf("absolute context rewritten: %+v", runtime.specs)
	}
}

func TestProfileReusesDeclaredCredentialsWithUnchangedSource(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCPs = p.MCPs[:1]
	p.Sets[0].MCPs = []string{"alpha"}
	p.MCPs[0].Actions = map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}
	p.MCPs[0].CredentialFiles = []string{"credential.bin"}
	p.Inputs = append(p.Inputs, catalog.Input{Name: "source_file", Type: "file"})
	q.Inputs = map[string]any{"source_file": filepath.Join(t.TempDir(), "deleted-original")}
	key, _ := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	s.Store.SaveAnswers(key, q.Inputs)
	child := mcpProfileKey(key, *p, p.MCPs[0])
	os.MkdirAll(s.Store.AuthDir(child), 0700)
	os.WriteFile(filepath.Join(s.Store.AuthDir(child), "credential.bin"), []byte("managed-fixture"), 0600)
	runner := &countingPrepareExecutor{}
	s.Options.Runner = runner
	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 {
		t.Fatal("unchanged missing source was imported again")
	}
}

func TestProfileApplyWaitsForWholeOperationStateLock(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Store.WithLock(context.Background(), func() error { close(held); <-release; return nil })
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := s.ApplyProfile(ctx, q)
	close(release)
	<-done
	if err == nil || len(a.calls) > 0 {
		t.Fatalf("mutation escaped operation lock: %v %v", err, a.calls)
	}
}
func TestProfileCLIPathsResolvedBeforeSaving(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	s.Source.Catalog[0].Inputs = append(s.Source.Catalog[0].Inputs, catalog.Input{Name: "folder", Type: "directory", Multiple: true})
	q.Inputs = map[string]any{"folder": []string{"./relative"}}
	q.ItemIDs = []string{}
	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	key, _ := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	answers, _ := s.Store.Answers(key)
	got, _ := answers["folder"].([]any)
	if len(got) != 1 || !filepath.IsAbs(got[0].(string)) {
		t.Fatalf("relative CLI path persisted: %v", answers)
	}
}
func TestProfileRegistrationNamesValidatedBeforeEffects(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	for n := range p.MCPs {
		p.MCPs[n].RegistrationNameInput = "label"
	}
	_, err := s.ApplyProfile(context.Background(), q)
	if err == nil || len(a.calls) != 0 || s.Options.Runtime.(*fakeRuntime).starts != 0 {
		t.Fatalf("duplicate names reached effects: err=%v calls=%v", err, a.calls)
	}
}

func TestProfileApplyHoldsLockDuringAdapterEffects(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	a.inspect = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if err := s.Store.WithLock(ctx, func() error { return nil }); err == nil {
			t.Error("operation lock released before adapter effects")
		}
	}
	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
}
