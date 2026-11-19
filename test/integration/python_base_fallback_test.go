package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrefetchPythonBasesFallsBackToSameOfficialDigestOnQuotaExhaustion(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	fakeBin := t.TempDir()
	program := `package main
import (
  "fmt"
  "os"
  "strings"
)
func main() {
  args := os.Args[1:]
  f, err := os.OpenFile(os.Getenv("DOCKER_CALLS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil { panic(err) }
  defer f.Close()
  fmt.Fprintln(f, strings.Join(args, " "))
  joined := strings.Join(args, " ")
  if strings.Contains(joined, "public.ecr.aws/") && strings.Contains(joined, "pull") {
    fmt.Fprintln(os.Stderr, "429 Too Many Requests: Data limit exceeded")
    os.Exit(1)
  }
  if strings.Contains(joined, "image inspect") {
    for _, arg := range args {
      if strings.HasPrefix(arg, "docker.io/library/python:") {
        digest := strings.SplitN(arg, "@", 2)[1]
        fmt.Printf("docker.io/library/python@%s\n", digest)
      }
    }
  }
}
`
	source := filepath.Join(work, "fake-docker.go")
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	dockerPath := filepath.Join(fakeBin, "docker")
	if runtime.GOOS == "windows" {
		dockerPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", dockerPath, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake Docker command: %s: %v", output, err)
	}
	sleepSource := filepath.Join(work, "fake-sleep.go")
	if err := os.WriteFile(sleepSource, []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sleepPath := filepath.Join(fakeBin, "sleep")
	if runtime.GOOS == "windows" {
		sleepPath += ".exe"
	}
	buildSleep := exec.Command("go", "build", "-o", sleepPath, sleepSource)
	if output, err := buildSleep.CombinedOutput(); err != nil {
		t.Fatalf("build fake sleep command: %s: %v", output, err)
	}
	script, err := filepath.Abs(filepath.Join(root, "tools", "prefetch-python-bases.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, platform, ecr, hub, outputKey, envKey string
	}{
		{
			name: "python 3.14 alpine amd64", platform: "linux/amd64",
			ecr:       "public.ecr.aws/docker/library/python:3.14-alpine@sha256:f6a589d43c42b9e7f7dc67a12d37132491f362859a5d750607710cc56da3bc72",
			hub:       "docker.io/library/python:3.14-alpine@sha256:f6a589d43c42b9e7f7dc67a12d37132491f362859a5d750607710cc56da3bc72",
			outputKey: "python_3_14_alpine", envKey: "AACT_PYTHON_BASE_IMAGE_3_14_ALPINE",
		},
		{
			name: "python 3.13 slim arm64", platform: "linux/arm64",
			ecr:       "public.ecr.aws/docker/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f",
			hub:       "docker.io/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f",
			outputKey: "python_3_13_slim", envKey: "AACT_PYTHON_BASE_IMAGE_3_13_SLIM",
		},
		{
			name: "python 3.12 slim amd64", platform: "linux/amd64",
			ecr:       "public.ecr.aws/docker/library/python:3.12-slim@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1",
			hub:       "docker.io/library/python:3.12-slim@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1",
			outputKey: "python_3_12_slim", envKey: "AACT_PYTHON_BASE_IMAGE_3_12_SLIM",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logPath := filepath.Join(work, strings.ReplaceAll(tc.name, " ", "-")+"-docker-args.txt")
			outputPath := filepath.Join(work, strings.ReplaceAll(tc.name, " ", "-")+"-github-output.txt")
			envPath := filepath.Join(work, strings.ReplaceAll(tc.name, " ", "-")+"-github-env.txt")
			cmd := snapshotScriptCommand(t, work, script, tc.platform, tc.ecr)
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"DOCKER_CALLS="+logPath,
				"GITHUB_OUTPUT="+outputPath,
				"GITHUB_ENV="+envPath,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("prefetch helper failed after quota exhaustion: %s: %v", output, err)
			}
			calls, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(calls), "pull --platform "+tc.platform+" "+tc.hub) || !strings.Contains(string(calls), "image inspect --format") {
				t.Fatalf("quota fallback did not pull and verify the same official digest: %s", calls)
			}
			gotOutput, err := os.ReadFile(outputPath)
			if err != nil || string(gotOutput) != tc.outputKey+"="+tc.hub+"\n" {
				t.Fatalf("GitHub Actions output = %q, %v", gotOutput, err)
			}
			gotEnv, err := os.ReadFile(envPath)
			if err != nil || string(gotEnv) != tc.envKey+"="+tc.hub+"\n" {
				t.Fatalf("GitHub Actions environment = %q, %v", gotEnv, err)
			}
		})
	}
}
