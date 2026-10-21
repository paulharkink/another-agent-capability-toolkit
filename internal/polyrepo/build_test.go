package polyrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fixtureBun struct {
	fail  bool
	calls int
}

func (f *fixtureBun) Run(ctx context.Context, args []string, cwd string, _ []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("injected source build failure")
	}
	if len(args) > 1 && args[0] == "bun" && args[1] == "run" {
		if err := os.MkdirAll(filepath.Join(cwd, "dist"), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(cwd, "dist", "index.js"), []byte("export default {};"), 0600); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func TestBuildCourierUsesPrivateSourceCopyAndImmutableStateOutput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "courier")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"scripts":{"build":"fixture"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureBun{}
	entry, revision, err := BuildCourier(context.Background(), filepath.Join(root, "state"), source, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if revision != "local-unversioned" || runner.calls != 3 {
		t.Fatalf("unexpected builder outcome: revision=%q calls=%d", revision, runner.calls)
	}
	if b, e := os.ReadFile(entry); e != nil || string(b) != "export default {};" {
		t.Fatalf("entrypoint: %q %v", b, e)
	}
	if _, e := os.Stat(filepath.Join(source, "dist")); !os.IsNotExist(e) {
		t.Fatalf("builder modified source checkout, dist stat=%v", e)
	}
	if _, _, err = BuildCourier(context.Background(), filepath.Join(root, "state"), source, &fixtureBun{fail: true}, nil); err == nil {
		t.Fatal("injected build failure was hidden")
	}
	if _, err = os.Stat(entry); err != nil {
		t.Fatalf("failed build removed previous immutable output: %v", err)
	}
}
