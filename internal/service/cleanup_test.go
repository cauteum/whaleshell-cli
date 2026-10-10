package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if logPath := os.Getenv("CAUTEM_FAKE_DOCKER_LOG"); logPath != "" {
		args := os.Args[1:]
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(1)
		}
		_, err = fmt.Fprintln(f, strings.Join(args, " "))
		if closeErr := f.Close(); err != nil || closeErr != nil {
			os.Exit(1)
		}
		switch {
		case len(args) >= 2 && args[0] == "volume" && args[1] == "ls":
			fmt.Print("anonymous-a\nanonymous-b\n")
		case len(args) >= 2 && args[0] == "ps" && args[1] == "-aq":
			fmt.Print("container-a\n")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCleanupDockerTestResourcesIsScopedAndDryRunByDefault(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "docker.log")
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	docker := filepath.Join(binDir, dockerTestExecutableName())
	output, err := os.OpenFile(docker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAUTEM_FAKE_DOCKER_LOG", logPath)

	a := New()
	if err := a.CleanupDockerTestResources(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if strings.Contains(log, "volume rm") || strings.Contains(log, "rm -f") || strings.Contains(log, "prune") {
		t.Fatalf("dry-run performed destructive docker operation: %q", log)
	}

	if err := a.CleanupDockerTestResources(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log = string(data)
	for _, want := range []string{
		"volume rm anonymous-a",
		"volume rm anonymous-b",
		"rm -f container-a",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("docker operation %q missing from log %q", want, log)
		}
	}
	if strings.Contains(log, "system prune") || strings.Contains(log, "volume rm named") {
		t.Fatalf("cleanup escaped scoped resource set: %q", log)
	}
}
