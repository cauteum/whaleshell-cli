package cli

import (
	"fmt"
	"strings"
)

// wantsHelp reports whether argv asks for help (-h/--help/help).
func wantsHelp(args []string) bool {
	for _, a := range args {
		switch a {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}

// helpPath strips help flags and returns the command path for nested help.
func helpPath(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "-h", "--help", "help":
			continue
		default:
			out = append(out, a)
		}
	}
	return out
}

func printHelp(args []string) error {
	path := helpPath(args)
	fmt.Print(helpText(path))
	return nil
}

func helpText(path []string) string {
	if len(path) == 0 {
		return rootHelp
	}
	key := strings.Join(path, " ")
	if t, ok := helpTree[key]; ok {
		return t
	}
	for i := len(path) - 1; i >= 1; i-- {
		parent := strings.Join(path[:i], " ")
		if t, ok := helpTree[parent]; ok {
			return t
		}
	}
	if t, ok := helpTree[path[0]]; ok {
		return t
	}
	return rootHelp
}

const rootHelp = `cautem — OpenShell-compatible agent sandbox CLI

Usage:
  cautem [global flags] <command> [flags]

Global flags:
  -g, --gateway NAME     Select gateway (or OPENSHELL_GATEWAY / CAUTEM_GATEWAY)
  --workspace NAME       Default workspace (or OPENSHELL_WORKSPACE)
  -o, --output FORMAT    text|json|yaml

Commands:
  sandbox (sb)       Create and manage sandboxes
  exec               Execute a command in a sandbox
  provider           Provider instances and profiles
  policy (pol)       Network policy get/set/update
  gateway (gw)       Add/select/login gateways
  workspace (ws)     Workspaces and members
  service (svc)      Expose sandbox ports via *.openshell.localhost
  forward (fwd)      TCP port forwards
  inference          Route inference.local
  settings           Gateway/local settings
  logs (lg)          Sandbox logs
  status             Gateway connectivity
  health             Docker/Podman and gateway health probe
  init               Write an agent starter policy
  doctor (dr)        Environment checks
  whoami             Identity (supports -o json)
  term               Interactive TUI
  install            Install CLI + ensure local gateway
  completions        Shell completions
  version            Print version

Run 'cautem <command> --help' for details.
`

var helpTree = map[string]string{
	"sandbox": `cautem sandbox — manage sandboxes

Usage:
  cautem sandbox create|list|get|stop|start|delete|exec|connect|upload|download|ssh-config|provider|template …

Aliases: sb

Examples:
  cautem sandbox create --name app --from ollama
  cautem sandbox list
  cautem sandbox exec app -- ls /
`,
	"sandbox create": `cautem sandbox create — create a sandbox

Usage:
  cautem sandbox create --name NAME [flags]

Flags (OpenShell-aligned):
  --name NAME
  --from base|ollama|cursor|claude|…
  --image IMAGE
  --policy PATH
  --cpu N --memory SIZE   (or set defaults.memory in config / CAUTEM_DEFAULT_MEMORY)
  --pids-limit N          (-1 unlimited; default 2048 via driver)
  --provider NAME (repeatable)
  --forward PORT (repeatable)
  --workspace PATH
  --label KEY=VALUE
  --driver-config-json JSON
`,
	"sandbox template": `cautem sandbox template — workload templates

Usage:
  cautem sandbox template create|list|get|delete …
`,
	"sandbox provider": `cautem sandbox provider — attach providers to a sandbox

Usage:
  cautem sandbox provider list|attach|detach …
`,
	"provider": `cautem provider — provider instances

Usage:
  cautem provider create|list|get|update|delete|profile|refresh|effective …

Examples:
  cautem provider create --name gh --type github --from-existing
  cautem provider refresh configure NAME --credential-key K --strategy oauth2-refresh-token
`,
	"provider profile": `cautem provider profile — custom provider YAML profiles

Usage:
  cautem profile list|show|import|update|export|delete|lint …
  cautem profile import --url https://example.org/profile.yaml
  cautem profile import --from ./provider-profiles
`,
	"profile": `cautem profile — reusable provider definitions

Usage:
  cautem profile list|show|import|update|export|delete|lint …
`,
	"provider refresh": `cautem provider refresh — credential refresh strategies

Usage:
  cautem provider refresh status|configure|rotate|delete …

Strategies: env | oauth2-refresh-token | oauth2-client-credentials | aws-sts-assume-role
`,
	"policy": `cautem policy — network policy

Usage:
  cautem policy get|set|update|list|delete|check …
`,
	"gateway": `cautem gateway — manage gateways

Usage:
  cautem gateway ensure|add|remove|select|info|list|login|logout

  ensure   start/select local gateway on 127.0.0.1:7443 if needed
`,
	"workspace": `cautem workspace — workspaces (gateway-backed)

Usage:
  cautem workspace create --name NAME
  cautem workspace list|get|delete NAME
  cautem workspace member add|remove|list …
`,
	"workspace member": `cautem workspace member — manage members

Usage:
  cautem workspace member add --workspace NAME --subject SUBJECT [--role user|admin]
  cautem workspace member remove --workspace NAME --subject SUBJECT
  cautem workspace member list --workspace NAME
`,
	"service": `cautem service — expose HTTP services

Usage:
  cautem service expose <sandbox> <port> [name]
  cautem service list|get|delete …

Edge URL (gateway Host router):
  http://<name>.openshell.localhost:<gateway-port>/
`,
	"forward": `cautem forward — TCP forwards into a sandbox

Usage:
  cautem forward start <host-port> <sandbox> [-d]
  cautem forward stop <id>
  cautem forward list
`,
	"inference": `cautem inference — inference.local routing

Usage:
  cautem inference get|set|update|list|show|local
`,
	"settings": `cautem settings — key/value settings

Usage:
  cautem settings get|set|delete …
`,
	"logs": `cautem logs — sandbox logs

Usage:
  cautem logs <name> [--tail] [-n N] [--since 5m]
`,
	"doctor": `cautem doctor — environment checks

Usage:
  cautem doctor check
  cautem doctor cleanup [--dry-run|--yes]

cleanup is a scoped dry-run by default. With --yes it removes only dangling
anonymous Testcontainers volumes and stopped containers labeled cautem=1.
`,
	"install": `cautem install — install CLI binary and ensure local gateway

Usage:
  cautem install [--force]

Copies cautem to ~/.local/share/cautem/bin, symlinks ~/.local/bin/cautem,
then starts/selects a local cautem-gateway if needed.
`,
	"whoami": `cautem whoami — print identity

Usage:
  cautem whoami
  cautem -o json whoami
`,
	"completions": `cautem completions — shell completions

Usage:
  cautem completions <bash|zsh|fish|powershell>
`,
	"status": `cautem status — gateway connectivity

Usage:
  cautem status
  cautem -o json status
`,
	"health": `cautem health — engine and gateway health probe

Usage:
  cautem health
  cautem -o json health
`,
	"init": `cautem init — write an agent starter policy

Usage:
  cautem init --agent cursor [--dir DIR] [--force]
`,
	"exec": `cautem exec — execute a command in a sandbox

Usage:
  cautem exec [--name] NAME -- COMMAND [ARG ...]
`,
	"term": `cautem term — interactive TUI

Usage:
  cautem term
`,
	"rule": `cautem rule — approval rules (MVP)

Usage:
  cautem rule get|approve|approve-all|reject|history|clear …
`,
}
