package cli

import (
	"strings"
	"testing"

	"github.com/cautem/cautem-cli/internal/service"
)

func TestHelpNested(t *testing.T) {
	root := helpText(nil)
	if root == "" || !strings.Contains(root, "cautem") {
		t.Fatal("root help empty")
	}
	sb := helpText([]string{"sandbox"})
	if !strings.Contains(sb, "sandbox create") {
		t.Fatalf("sandbox help: %s", sb)
	}
	create := helpText([]string{"sandbox", "create"})
	if !strings.Contains(create, "--name") {
		t.Fatalf("create help: %s", create)
	}
	if !wantsHelp([]string{"sandbox", "--help"}) {
		t.Fatal("wantsHelp")
	}
	doctor := helpText([]string{"doctor"})
	if !strings.Contains(doctor, "--dry-run|--yes") {
		t.Fatalf("doctor help: %s", doctor)
	}
	if err := runDoctor(nil, []string{"cleanup", "--yes", "--dry-run"}); err == nil {
		t.Fatal("doctor cleanup accepted conflicting confirmation flags")
	}
	path := helpPath([]string{"service", "expose", "-h"})
	if len(path) != 2 || path[0] != "service" {
		t.Fatalf("helpPath %#v", path)
	}
}

func TestSandboxCreateRejectsRemovedNoOpSSHFlag(t *testing.T) {
	err := runSandbox(&service.App{}, []string{"create", "--ssh"})
	if err == nil || !strings.Contains(err.Error(), "--ssh was removed") {
		t.Fatalf("error=%v, want explicit removed-flag error", err)
	}
}

func TestCompletionsUsecautemFunctionName(t *testing.T) {
	for _, script := range []string{completionsBash, completionsZsh} {
		if strings.Contains(script, "_osg") || !strings.Contains(script, "_cautem") {
			t.Fatalf("completion function name is stale: %q", script)
		}
	}
}

func TestCompletionScriptsIncludeRootCommands(t *testing.T) {
	commands := []string{"version", "sandbox", "exec", "provider", "profile", "policy", "gateway", "logs", "term", "status", "health", "init", "doctor", "whoami", "workspace", "forward", "service", "settings", "inference", "rule", "install", "completions", "ssh-proxy"}
	for name, script := range map[string]string{
		"bash":       completionsBash,
		"zsh":        completionsZsh,
		"fish":       completionsFish,
		"powershell": completionsPowerShell,
	} {
		for _, command := range commands {
			if !strings.Contains(script, command) {
				t.Errorf("%s completions do not include %q", name, command)
			}
		}
	}
}
