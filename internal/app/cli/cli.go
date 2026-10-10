// Package cli is the thin cobra-facing layer for the cautem binary.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/cautem/cautem-cli/internal/global"
	"github.com/cautem/cautem-cli/internal/logger"
	"github.com/cautem/cautem-cli/internal/osargs"
	"github.com/cautem/cautem-cli/internal/providerflags"
	"github.com/cautem/cautem-cli/internal/service"
	"github.com/cautem/cautem-cli/internal/storage/templates"
	tuipkg "github.com/cautem/cautem-cli/internal/tui"
	"golang.org/x/term"
)

// Execute runs the CLI with OpenShell command names and argv shapes.
func Execute(ctx context.Context, args []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g, rest := global.Parse(args)
	a := service.New()
	a.SetCommandContext(ctx)
	a.ApplyGlobal(g)
	return execute(a, rest)
}

func execute(a *service.App, args []string) error {
	if wantsHelp(args) {
		return printHelp(args)
	}
	if len(args) == 0 {
		fmt.Println(a.Banner())
		fmt.Println("commands: sandbox|exec|provider|profile|policy|gateway|logs|term|status|health|init|doctor|whoami|workspace|forward|service|settings|inference|…")
		fmt.Println("run 'cautem --help' for full usage")
		return nil
	}
	log := logger.FromContext(a.CommandContext()).With(slog.String("op", "cli.execute"), slog.String("cmd", args[0]))
	log.Info("running command")
	switch args[0] {
	case "version":
		fmt.Println(a.Version())
		return nil
	case "health":
		return a.Health()
	case "init":
		return runInit(a, args[1:])
	case "status":
		return a.Status()
	case "doctor", "dr":
		return runDoctor(a, args[1:])
	case "policy", "pol":
		return runPolicy(a, args[1:])
	case "sandbox", "sb":
		return runSandbox(a, args[1:])
	case "exec":
		return runExec(a, args[1:])
	case "term":
		return runTerm(a, args[1:])
	case "gateway", "gw":
		return runGateway(a, args[1:])
	case "logs", "lg":
		return runLogs(a, args[1:])
	case "provider":
		return runProvider(a, args[1:])
	case "profile":
		return runProvider(a, append([]string{"profile"}, args[1:]...))
	case "whoami":
		return a.Whoami()
	case "workspace", "ws":
		return runWorkspace(a, args[1:])
	case "settings":
		return runSettings(a, args[1:])
	case "forward", "fwd":
		return runForward(a, args[1:])
	case "service", "svc":
		return runService(a, args[1:])
	case "rule", "rl":
		return runRule(a, args[1:])
	case "inference":
		return runInference(a, args[1:])
	case "completions":
		return runCompletions(args[1:])
	case "ssh-proxy":
		return runSSHProxy(a, args[1:])
	case "proxy":
		// Internal: Docker sidecar entrypoint (`/cautem/cautem proxy --listen … --policy …`).
		return runProxy(a, args[1:])
	case "install":
		return runInstall(a, args[1:])
	default:
		return fmt.Errorf("unknown command %q (try: status|health|init|exec|doctor|sandbox|provider|policy|gateway|logs|workspace|forward|service|settings|whoami|term)", args[0])
	}
}

func runDoctor(a *service.App, args []string) error {
	// OpenShell: doctor check (doctor alone → check)
	if len(args) == 0 || args[0] == "check" {
		return a.Doctor()
	}
	if args[0] == "cleanup" {
		confirm := false
		explicitDryRun := false
		for _, arg := range args[1:] {
			switch arg {
			case "--yes", "-y":
				if explicitDryRun {
					return fmt.Errorf("usage: cautem doctor cleanup [--dry-run|--yes]")
				}
				confirm = true
			case "--dry-run":
				if confirm {
					return fmt.Errorf("usage: cautem doctor cleanup [--dry-run|--yes]")
				}
				explicitDryRun = true
			default:
				return fmt.Errorf("usage: cautem doctor cleanup [--dry-run|--yes]")
			}
		}
		return a.CleanupDockerTestResources(a.CommandContext(), confirm)
	}
	return fmt.Errorf("usage: cautem doctor check")
}

func runCompletions(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem completions <bash|zsh|fish|powershell>")
	}
	shell := strings.ToLower(args[0])
	switch shell {
	case "bash":
		fmt.Print(completionsBash)
	case "zsh":
		fmt.Print(completionsZsh)
	case "fish":
		fmt.Print(completionsFish)
	case "powershell":
		fmt.Print(completionsPowerShell)
	default:
		return fmt.Errorf("unsupported shell %q (bash|zsh|fish|powershell)", shell)
	}
	return nil
}

func runInit(a *service.App, args []string) error {
	opt := service.InitOpts{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 >= len(args) {
				return fmt.Errorf("usage: cautem init --agent cursor [--dir DIR] [--force]")
			}
			i++
			opt.Agent = args[i]
		case "--dir":
			if i+1 >= len(args) {
				return fmt.Errorf("usage: cautem init --agent cursor [--dir DIR] [--force]")
			}
			i++
			opt.Dir = args[i]
		case "--force", "-f":
			opt.Force = true
		default:
			return fmt.Errorf("usage: cautem init --agent cursor [--dir DIR] [--force]")
		}
	}
	if strings.TrimSpace(opt.Agent) == "" {
		return fmt.Errorf("usage: cautem init --agent cursor [--dir DIR] [--force]")
	}
	return a.Init(opt)
}

func runProvider(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem provider create --name NAME --type PROFILE [--from-existing|--credential KEY]")
	}
	switch args[0] {
	case "list-profiles":
		rest, scope, workspace, err := parseProfileScope(a, args[1:])
		if err != nil {
			return err
		}
		if len(rest) != 0 {
			return fmt.Errorf("usage: cautem provider list-profiles [--workspace NAME|--global]")
		}
		return a.ProviderProfileList(scope, workspace)
	case "profile":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem provider profile list|show|import|export|delete|lint …")
		}
		rest, scope, workspace, err := parseProfileScope(a, args[2:])
		if err != nil {
			return err
		}
		switch args[1] {
		case "list", "ls":
			if len(rest) != 0 {
				return fmt.Errorf("usage: cautem profile list [--workspace NAME|--global]")
			}
			return a.ProviderProfileList(scope, workspace)
		case "show", "describe", "export":
			id, outFmt, err := parseProfileIO(rest, false)
			if err != nil {
				return err
			}
			if id == "" {
				return fmt.Errorf("usage: cautem provider profile export <id> [-o yaml|json]")
			}
			return a.ProviderProfileShowFmt(id, outFmt, scope, workspace)
		case "import":
			path, _, err := parseProfileIO(rest, true)
			if err != nil {
				return err
			}
			if path == "" {
				return fmt.Errorf("usage: cautem provider profile import -f <file.yaml>")
			}
			return a.ProviderProfileImport(path, scope, workspace)
		case "update":
			path, _, err := parseProfileIO(rest, true)
			if err != nil {
				return err
			}
			if path == "" {
				return fmt.Errorf("usage: cautem provider profile update -f <file.yaml>")
			}
			return a.ProviderProfileUpdate(path, scope, workspace)
		case "delete":
			if len(args) < 3 {
				return fmt.Errorf("usage: cautem provider profile delete <id>")
			}
			if len(rest) != 1 {
				return fmt.Errorf("usage: cautem profile delete <id> [--workspace NAME|--global]")
			}
			return a.ProviderProfileDelete(rest[0], scope, workspace)
		case "lint":
			path, _, err := parseProfileIO(rest, true)
			if err != nil {
				return err
			}
			if path == "" {
				return fmt.Errorf("usage: cautem provider profile lint -f <file.yaml>")
			}
			return a.ProviderProfileLint(path)
		default:
			return fmt.Errorf("unknown profile subcommand %q", args[1])
		}
	case "create":
		parsed, err := providerflags.ParseCreate(args[1:], os.LookupEnv)
		if err != nil {
			return err
		}
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--credential" && strings.Contains(args[i+1], "=") {
				fmt.Fprintln(os.Stderr, "warn: --credential KEY=VALUE may appear in shell history/ps; prefer bare --credential KEY")
				break
			}
		}
		return a.ProviderCreate(parsed)
	case "update":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem provider update <name> [--from-existing|--credential KEY[=VALUE]]")
		}
		name := args[1]
		fromExisting := false
		credentials := map[string]string{}
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--from-existing":
				fromExisting = true
			case "--credential":
				i++
				if i >= len(args) {
					return fmt.Errorf("--credential needs KEY or KEY=VALUE")
				}
				raw := args[i]
				if k, v, ok := strings.Cut(raw, "="); ok {
					fmt.Fprintln(os.Stderr, "warn: --credential KEY=VALUE may appear in shell history/ps; prefer bare --credential KEY")
					credentials[strings.TrimSpace(k)] = v
				} else {
					key := strings.TrimSpace(raw)
					v, ok := os.LookupEnv(key)
					if !ok || strings.TrimSpace(v) == "" {
						return fmt.Errorf("--credential %s: env var not set on host", key)
					}
					credentials[key] = v
				}
			default:
				return fmt.Errorf("unknown flag %q", args[i])
			}
		}
		return a.ProviderUpdate(name, fromExisting, credentials)
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem provider get <name>")
		}
		return a.ProviderGet(args[1])
	case "delete", "rm":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem provider delete <name>")
		}
		return a.ProviderDelete(args[1])
	case "refresh":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem provider refresh <name>|status|configure|rotate|delete …")
		}
		switch args[1] {
		case "status":
			if len(args) < 3 {
				return fmt.Errorf("usage: cautem provider refresh status <name>")
			}
			return a.ProviderRefreshStatus(args[2])
		case "configure":
			if len(args) < 3 {
				return fmt.Errorf("usage: cautem provider refresh configure <name> --credential-key K --strategy S")
			}
			opt, err := providerflags.ParseRefreshConfigure(args[2], args[3:])
			if err != nil {
				return err
			}
			return a.ProviderRefreshConfigure(opt)
		case "rotate":
			if len(args) < 3 {
				return fmt.Errorf("usage: cautem provider refresh rotate <name> --credential-key K")
			}
			key, err := providerflags.ParseRefreshKey(args[2], args[3:])
			if err != nil {
				return err
			}
			return a.ProviderRefreshRotate(args[2], key)
		case "delete":
			if len(args) < 3 {
				return fmt.Errorf("usage: cautem provider refresh delete <name> --credential-key K")
			}
			key, err := providerflags.ParseRefreshKey(args[2], args[3:])
			if err != nil {
				return err
			}
			return a.ProviderRefreshDelete(args[2], key)
		default:
			return a.ProviderRefresh(args[1])
		}
	case "list", "ls":
		return a.ProviderList()
	case "effective":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem provider effective <sandbox>")
		}
		return a.ProviderEffective(args[1])
	default:
		return fmt.Errorf("unknown provider subcommand %q", args[0])
	}
}

func parseProfileScope(a *service.App, args []string) (rest []string, scope, workspace string, err error) {
	scope, workspace = "global", ""
	if a != nil && a.GlobalWorkspace != "" && a.GlobalWorkspace != "default" {
		scope, workspace = "workspace", a.GlobalWorkspace
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--global":
			scope, workspace = "global", ""
		case "--workspace":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return nil, "", "", fmt.Errorf("--workspace needs a name")
			}
			scope, workspace = "workspace", args[i]
		default:
			if after, ok := strings.CutPrefix(args[i], "--workspace="); ok {
				workspace = strings.TrimSpace(after)
				if workspace == "" {
					return nil, "", "", fmt.Errorf("--workspace needs a name")
				}
				scope = "workspace"
				continue
			}
			rest = append(rest, args[i])
		}
	}
	return rest, scope, workspace, nil
}

func runInstall(a *service.App, args []string) error {
	opt := service.InstallOpts{}
	for i := range args {
		switch args[i] {
		case "--force":
			opt.Force = true
		default:
			return fmt.Errorf("unknown flag %q (usage: cautem install [--force])", args[i])
		}
	}
	return a.Install(opt)
}

func runGateway(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem gateway ensure|add|remove|select|info|list|login|logout")
	}
	switch args[0] {
	case "ensure":
		return a.GatewayEnsure()
	case "add":
		parsed, err := osargs.ParseGatewayAdd(args[1:])
		if err != nil {
			return err
		}
		return a.GatewayAddParsed(parsed)
	case "remove", "rm", "delete":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		if name == "" {
			return fmt.Errorf("usage: cautem gateway remove [name]")
		}
		return a.GatewayRemove(name)
	case "select":
		if len(args) < 2 {
			return a.GatewayListRemote() // OpenShell: select without name lists
		}
		return a.GatewaySelect(args[1])
	case "status":
		return a.GatewayStatus()
	case "info":
		return a.GatewayInfo()
	case "list", "ls":
		return a.GatewayListRemote()
	case "login":
		token := ""
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			token = args[1]
		}
		return a.GatewayLoginInteractive(token)
	case "logout":
		return a.GatewayLogout()
	default:
		return fmt.Errorf("unknown gateway subcommand %q", args[0])
	}
}

func runWorkspace(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem workspace create|get|list|delete|member …")
	}
	switch args[0] {
	case "create":
		name := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--name":
				i++
				if i >= len(args) {
					return fmt.Errorf("--name needs a value")
				}
				name = args[i]
			default:
				if strings.HasPrefix(args[i], "-") {
					return fmt.Errorf("unknown flag %q", args[i])
				}
				if name != "" {
					return fmt.Errorf("unexpected argument %q", args[i])
				}
				name = args[i]
			}
		}
		if name == "" {
			return fmt.Errorf("usage: cautem workspace create --name NAME")
		}
		return a.WorkspaceCreate(name)
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem workspace get <name>")
		}
		return a.WorkspaceGet(args[1])
	case "list", "ls":
		return a.WorkspaceList()
	case "delete", "rm":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem workspace delete <name>")
		}
		return a.WorkspaceDelete(args[1])
	case "member":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem workspace member add|remove|list …")
		}
		switch args[1] {
		case "add":
			ws, subject, role, err := parseWorkspaceMemberFlags(args[2:], true)
			if err != nil {
				return err
			}
			return a.WorkspaceMemberAddRole(ws, subject, role)
		case "remove", "rm":
			ws, subject, _, err := parseWorkspaceMemberFlags(args[2:], false)
			if err != nil {
				return err
			}
			return a.WorkspaceMemberRemove(ws, subject)
		case "list", "ls":
			ws := ""
			for i := 2; i < len(args); i++ {
				switch args[i] {
				case "--workspace":
					i++
					if i >= len(args) {
						return fmt.Errorf("--workspace needs a value")
					}
					ws = args[i]
				default:
					if strings.HasPrefix(args[i], "-") {
						return fmt.Errorf("unknown flag %q", args[i])
					}
					ws = args[i]
				}
			}
			if ws == "" {
				return fmt.Errorf("usage: cautem workspace member list --workspace NAME")
			}
			return a.WorkspaceMemberList(ws)
		default:
			return fmt.Errorf("unknown member subcommand %q", args[1])
		}
	default:
		return fmt.Errorf("unknown workspace subcommand %q", args[0])
	}
}

// parseWorkspaceMemberFlags accepts OpenShell `--workspace --subject --role` or positional WS SUBJECT.
func parseWorkspaceMemberFlags(args []string, needRole bool) (ws, subject, role string, err error) {
	role = "user"
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workspace":
			i++
			if i >= len(args) {
				return "", "", "", fmt.Errorf("--workspace needs a value")
			}
			ws = args[i]
		case "--subject":
			i++
			if i >= len(args) {
				return "", "", "", fmt.Errorf("--subject needs a value")
			}
			subject = args[i]
		case "--role":
			i++
			if i >= len(args) {
				return "", "", "", fmt.Errorf("--role needs user|admin")
			}
			role = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", "", fmt.Errorf("unknown flag %q", args[i])
			}
			positionals = append(positionals, args[i])
		}
	}
	if ws == "" && len(positionals) > 0 {
		ws = positionals[0]
		positionals = positionals[1:]
	}
	if subject == "" && len(positionals) > 0 {
		subject = positionals[0]
	}
	if ws == "" || subject == "" {
		return "", "", "", fmt.Errorf("usage: cautem workspace member add --workspace NAME --subject SUBJECT [--role user|admin]")
	}
	if needRole && role != "user" && role != "admin" {
		return "", "", "", fmt.Errorf("--role must be user|admin")
	}
	return ws, subject, role, nil
}

func runSettings(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem settings get|set|delete …")
	}
	switch args[0] {
	case "get":
		name := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--global", "--json":
			default:
				if strings.HasPrefix(args[i], "-") {
					return fmt.Errorf("unknown flag %q", args[i])
				}
				name = args[i]
			}
		}
		if name == "" {
			return a.SettingsGet("")
		}
		return a.SettingsGet(name)
	case "set":
		parsed, err := osargs.ParseSettingsSet(args[1:])
		if err != nil {
			return err
		}
		return a.SettingsSet(parsed.Key, parsed.Value)
	case "delete", "rm":
		key := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--key":
				i++
				if i >= len(args) {
					return fmt.Errorf("--key needs a value")
				}
				key = args[i]
			case "--global":
			default:
				if !strings.HasPrefix(args[i], "-") && key == "" {
					key = args[i]
				}
			}
		}
		if key == "" {
			return fmt.Errorf("usage: cautem settings delete --key KEY [--global]")
		}
		return a.SettingsDelete(key)
	default:
		return fmt.Errorf("unknown settings subcommand %q", args[0])
	}
}

func runForward(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem forward start|stop|list …")
	}
	switch args[0] {
	case "start":
		parsed, err := osargs.ParseForwardStart(args[1:])
		if err != nil {
			return err
		}
		if parsed.Name == "" {
			return fmt.Errorf("usage: cautem forward start [bind:]port <sandbox> [-d]")
		}
		return a.ForwardStart(parsed.Name, parsed.Port, parsed.Port, parsed.Background)
	case "stop":
		parsed, err := osargs.ParseForwardStop(args[1:])
		if err != nil {
			return err
		}
		id := parsed.Port
		if parsed.Name != "" {
			id = parsed.Name + ":" + parsed.Port
		}
		return a.ForwardStop(id)
	case "list", "ls":
		return a.ForwardList()
	case "service":
		return fmt.Errorf("forward service: use cautem service expose")
	default:
		return fmt.Errorf("unknown forward subcommand %q", args[0])
	}
}

func runService(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem service expose|list|get|delete …")
	}
	switch args[0] {
	case "expose":
		parsed, err := osargs.ParseServiceExpose(args[1:])
		if err != nil {
			return err
		}
		return a.ServiceExpose(parsed.Sandbox, parsed.Service, parsed.Port)
	case "list", "ls":
		return a.ServiceList()
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem service get <sandbox> [service]")
		}
		name := "default"
		if len(args) > 2 {
			name = args[2]
		}
		return a.ServiceGet(name)
	case "delete", "rm":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem service delete <sandbox> [service]")
		}
		name := "default"
		if len(args) > 2 {
			name = args[2]
		}
		return a.ServiceDelete(name)
	default:
		return fmt.Errorf("unknown service subcommand %q", args[0])
	}
}

func runRule(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem rule get|approve|reject|history|clear …")
	}
	switch args[0] {
	case "get", "list", "ls":
		status := ""
		sandbox := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--status":
				i++
				if i >= len(args) {
					return fmt.Errorf("--status needs pending|approved|rejected")
				}
				status = args[i]
			default:
				if strings.HasPrefix(args[i], "-") {
					return fmt.Errorf("unknown flag %q", args[i])
				}
				sandbox = args[i]
			}
		}
		return a.RuleListFilter(sandbox, status)
	case "history":
		sandbox := ""
		if len(args) > 1 {
			sandbox = args[1]
		}
		return a.RuleListFilter(sandbox, "")
	case "approve":
		id, reason, err := parseRuleAction(args[1:])
		if err != nil {
			return err
		}
		_ = reason
		return a.RuleApprove(id)
	case "approve-all":
		sandbox := ""
		includeSecurityFlagged := false
		for _, arg := range args[1:] {
			switch {
			case arg == "--include-security-flagged":
				includeSecurityFlagged = true
			case strings.HasPrefix(arg, "-"):
				return fmt.Errorf("unknown flag %q", arg)
			case sandbox == "":
				sandbox = arg
			default:
				return fmt.Errorf("usage: cautem rule approve-all [NAME] [--include-security-flagged]")
			}
		}
		return a.RuleApproveAll(sandbox, includeSecurityFlagged)
	case "reject":
		id, reason, err := parseRuleAction(args[1:])
		if err != nil {
			return err
		}
		return a.RuleRejectReason(id, reason)
	case "clear":
		return a.RuleClear()
	default:
		return fmt.Errorf("unknown rule subcommand %q", args[0])
	}
}

func parseRuleAction(args []string) (id, reason string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--chunk-id":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("--chunk-id needs a value")
			}
			id = args[i]
		case "--reason":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("--reason needs a value")
			}
			reason = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("unknown flag %q", args[i])
			}
			if id == "" {
				id = args[i]
			}
		}
	}
	if id == "" {
		return "", "", fmt.Errorf("usage: cautem rule approve|reject [--chunk-id] ID [--reason TEXT]")
	}
	return id, reason, nil
}

func runLogs(a *service.App, args []string) error {
	parsed, err := osargs.ParseLogs(args)
	if err != nil {
		return err
	}
	opt := service.LogsOpts{
		Follow: parsed.Tail,
		All:    parsed.All,
		Since:  parsed.Since,
		Source: parsed.Source,
		Level:  parsed.Level,
	}
	if parsed.Name != "" {
		for n := range strings.SplitSeq(parsed.Name, ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				opt.Names = append(opt.Names, n)
			}
		}
	}
	if !opt.All && len(opt.Names) == 0 {
		return fmt.Errorf("usage: cautem logs <name> [--tail] [-n N] [--since 5m] [--source sandbox] [--level warn]")
	}
	return a.LogsOpts(opt)
}

func runInference(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem inference get|set|update|list|show|local")
	}
	switch args[0] {
	case "get":
		return a.InferenceRouteGet()
	case "set", "update":
		parsed, err := osargs.ParseInferenceSet(args[1:])
		if err != nil {
			return err
		}
		return a.InferenceRouteSet(parsed)
	case "list", "ls":
		return a.InferenceList()
	case "show":
		path := ""
		if len(args) > 1 {
			path = args[1]
		}
		return a.InferenceShow(path)
	case "local":
		return a.InferenceLocal()
	case "delete", "rm":
		return a.InferenceRouteDelete()
	default:
		return fmt.Errorf("unknown inference subcommand %q", args[0])
	}
}

func runPolicy(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem policy get|set|update|list|delete|check …")
	}
	switch args[0] {
	case "check": // cautem-only validation helper
		path := "policies/default.yaml"
		if len(args) > 1 {
			path = args[1]
		}
		return a.PolicyCheck(path)
	case "get":
		parsed, err := osargs.ParsePolicyGet(args[1:])
		if err != nil {
			return err
		}
		if parsed.Global {
			return a.PolicyGlobalGet()
		}
		if parsed.Rev > 0 {
			return a.PolicyGetRevision(parsed.Name, parsed.Rev)
		}
		return a.PolicyGet(parsed.Name, parsed.View)
	case "set":
		parsed, err := osargs.ParsePolicySet(args[1:])
		if err != nil {
			return err
		}
		if parsed.Global {
			return a.PolicyGlobalSet(parsed.Path)
		}
		return a.PolicySet(parsed.Name, parsed.Path, parsed.Wait)
	case "update":
		return runPolicyUpdate(a, args[1:])
	case "list", "ls":
		global := false
		name := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--global":
				global = true
			default:
				if strings.HasPrefix(args[i], "-") {
					return fmt.Errorf("unknown flag %q", args[i])
				}
				name = args[i]
			}
		}
		if global {
			fmt.Println("policy list --global: (use policy get --global for current YAML)")
			return a.PolicyGlobalGet()
		}
		if name == "" {
			return fmt.Errorf("usage: cautem policy list <sandbox>")
		}
		return a.PolicyList(name)
	case "delete", "rm":
		global := false
		for _, f := range args[1:] {
			if f == "--global" {
				global = true
			}
		}
		if !global {
			return fmt.Errorf("usage: cautem policy delete --global")
		}
		fmt.Println("policy delete --global: clearing gateway global policy")
		return a.PolicyGlobalClear()
	default:
		return fmt.Errorf("unknown policy subcommand %q", args[0])
	}
}

func runPolicyUpdate(a *service.App, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cautem policy update <sandbox> --add-endpoint SPEC [--add-allow SPEC] [--add-deny SPEC] [--binary PATH] [--wait]")
	}
	name := args[0]
	wait := false
	var endpoints, allows, denies, binaries []string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--add-endpoint":
			i++
			if i >= len(args) {
				return fmt.Errorf("--add-endpoint needs a value")
			}
			endpoints = append(endpoints, args[i])
		case "--add-allow":
			i++
			if i >= len(args) {
				return fmt.Errorf("--add-allow needs a value")
			}
			allows = append(allows, args[i])
		case "--add-deny":
			i++
			if i >= len(args) {
				return fmt.Errorf("--add-deny needs a value")
			}
			denies = append(denies, args[i])
		case "--binary":
			i++
			if i >= len(args) {
				return fmt.Errorf("--binary needs a path")
			}
			binaries = append(binaries, args[i])
		case "--wait":
			wait = true
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return a.PolicyUpdate(name, endpoints, allows, denies, binaries, wait)
}

func runSandbox(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem sandbox create|list|get|stop|start|delete|exec|connect|upload|download|ssh-config|provider")
	}
	switch args[0] {
	case "create":
		opt := service.SandboxCreateOpts{Workspace: ".", Labels: map[string]string{}}
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--" {
				opt.Argv = append([]string{}, rest[i+1:]...)
				break
			}
			switch rest[i] {
			case "--name":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--name needs a value")
				}
				opt.Name = rest[i]
			case "--image":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--image needs a value")
				}
				opt.Image = rest[i]
			case "--workspace":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--workspace needs a value")
				}
				opt.Workspace = rest[i]
			case "--policy":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--policy needs a value")
				}
				opt.Policy = rest[i]
			case "--i-know":
				opt.IKnow = true
			case "--no-proxy":
				opt.NoProxy = true
			case "--no-harden":
				opt.NoHarden = true
			case "--display":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--display needs a value (none|novnc)")
				}
				opt.Display = rest[i]
			case "--display-port":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--display-port needs a value")
				}
				var p int
				if _, err := fmt.Sscanf(rest[i], "%d", &p); err != nil || p <= 0 {
					return fmt.Errorf("invalid --display-port")
				}
				opt.DisplayPort = p
			case "--open-display":
				opt.OpenDisplay = true
			case "--label":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--label needs key=value")
				}
				k, v, ok := strings.Cut(rest[i], "=")
				if !ok || k == "" {
					return fmt.Errorf("--label needs key=value")
				}
				opt.Labels[k] = v
			case "--gateway":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--gateway needs a URL")
				}
				opt.GatewayURL = rest[i]
			case "--from":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--from needs an alias")
				}
				opt.From = rest[i]
			case "--ssh":
				return fmt.Errorf("sandbox create: --ssh was removed; SSH relay is configured automatically when a gateway is selected")
			case "--gpu":
				opt.GPU = true
			case "--cdi":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--cdi needs a device id (e.g. nvidia.com/gpu=all)")
				}
				opt.CDIDevices = append(opt.CDIDevices, rest[i])
			case "--no-volume":
				opt.NoVolume = true
			case "--no-host-internal":
				opt.NoHostInternal = true
			case "--provider":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--provider needs a profile id or instance name (e.g. github)")
				}
				opt.Providers = append(opt.Providers, rest[i])
			case "--auto-providers":
				opt.AutoProviders = true
			case "--no-auto-providers":
				opt.NoAutoProviders = true
			case "--no-credential-warnings":
				opt.NoCredentialWarnings = true
			case "--env":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--env needs KEY=VALUE")
				}
				k, v, ok := strings.Cut(rest[i], "=")
				if !ok || k == "" {
					return fmt.Errorf("--env needs KEY=VALUE")
				}
				if opt.Env == nil {
					opt.Env = map[string]string{}
				}
				opt.Env[k] = v
			case "--detach":
				opt.Detach = true
			case "--editor":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--editor needs vscode|cursor")
				}
				opt.Editor = rest[i]
			case "--forward":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--forward needs PORT")
				}
				var p int
				if _, err := fmt.Sscanf(rest[i], "%d", &p); err != nil || p <= 0 {
					return fmt.Errorf("invalid --forward port")
				}
				opt.Forwards = append(opt.Forwards, p)
			case "--upload":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--upload needs PATH")
				}
				opt.Upload = rest[i]
			case "--cpu":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--cpu needs a float")
				}
				var f float64
				if _, err := fmt.Sscanf(rest[i], "%f", &f); err != nil || f <= 0 {
					return fmt.Errorf("invalid --cpu")
				}
				opt.CPU = f
			case "--memory":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--memory needs a size (512m, 4g, …)")
				}
				opt.Memory = rest[i]
			case "--pids-limit":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--pids-limit needs an int (-1 unlimited, >0 limit)")
				}
				var n int64
				if _, err := fmt.Sscanf(rest[i], "%d", &n); err != nil || n == 0 {
					return fmt.Errorf("invalid --pids-limit (use -1 for unlimited or positive int)")
				}
				opt.PidsLimit = n
			case "--template":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--template needs NAME")
				}
				opt.Template = rest[i]
			case "--driver-config-json":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--driver-config-json needs JSON")
				}
				opt.DriverConfigJSON = rest[i]
			case "--approval-mode":
				i++
				if i >= len(rest) {
					return fmt.Errorf("--approval-mode needs manual|auto")
				}
				opt.ApprovalMode = rest[i]
			case "--no-keep":
				opt.NoKeep = true
			case "--tty":
				opt.ForceTTY = true
			default:
				return fmt.Errorf("unknown flag %q", rest[i])
			}
		}
		return a.SandboxCreate(opt)
	case "list", "ls":
		opt := service.SandboxListOpts{Limit: 100, Output: "table"}
		seen := map[string]bool{}
		for i := 1; i < len(args); i++ {
			arg := args[i]
			flag := strings.SplitN(arg, "=", 2)[0]
			if seen[flag] {
				return fmt.Errorf("sandbox list: duplicate flag %q", flag)
			}
			seen[flag] = true
			value := func() (string, error) {
				if eq := strings.IndexByte(arg, '='); eq >= 0 {
					return arg[eq+1:], nil
				}
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return "", fmt.Errorf("%s needs a value", arg)
				}
				i++
				return args[i], nil
			}
			switch flag {
			case "--limit", "--offset", "--selector", "-o", "--output":
				v, err := value()
				if err != nil {
					return err
				}
				switch flag {
				case "--limit":
					n, err := strconv.ParseUint(v, 10, 32)
					if err != nil {
						return fmt.Errorf("sandbox list: invalid --limit %q", v)
					}
					opt.Limit = uint32(n)
				case "--offset":
					n, err := strconv.ParseUint(v, 10, 32)
					if err != nil {
						return fmt.Errorf("sandbox list: invalid --offset %q", v)
					}
					opt.Offset = uint32(n)
				case "--selector":
					opt.Selector = v
				case "-o", "--output":
					opt.Output = v
				}
			case "--ids":
				opt.IDs = true
			case "--names":
				opt.Names = true
			case "--all-workspaces":
				opt.AllWorkspaces = true
			default:
				return fmt.Errorf("sandbox list: unknown argument %q", arg)
			}
		}
		if opt.IDs && opt.Names {
			return fmt.Errorf("sandbox list: --ids conflicts with --names")
		}
		if (opt.IDs || opt.Names) && seen["-o"] || (opt.IDs || opt.Names) && seen["--output"] {
			return fmt.Errorf("sandbox list: --ids/--names conflict with --output")
		}
		return a.SandboxList(opt)
	case "get", "status":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem sandbox get <name>")
		}
		return a.SandboxStatus(args[1])
	case "stop":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem sandbox stop <name>")
		}
		return a.SandboxStop(args[1])
	case "start":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem sandbox start <name>")
		}
		return a.SandboxStart(args[1])
	case "rm", "remove", "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem sandbox delete <name>")
		}
		return a.SandboxRemove(args[1])
	case "exec":
		return runExec(a, args[1:])
	case "upload", "download":
		parsed, err := osargs.ParseSandboxTransfer(args[1:])
		if err != nil {
			return err
		}
		dest := parsed.Dest
		if args[0] == "upload" {
			if dest == "" {
				dest = "/workspace"
			}
			return a.Copy(parsed.Path, parsed.Name+":"+dest)
		}
		if dest == "" {
			dest = "."
		}
		return a.Copy(parsed.Name+":"+parsed.Path, dest)
	case "ssh-config":
		parsed, err := osargs.ParseSandboxSSHConfig(args[1:])
		if err != nil {
			return err
		}
		return a.SandboxSSHConfig(parsed.Name, parsed.Install)
	case "connect":
		parsed, err := osargs.ParseSandboxConnect(args[1:])
		if err != nil {
			return err
		}
		return a.SandboxConnect(service.ConnectOpts{Name: parsed.Name, Editor: parsed.Editor, Argv: parsed.Argv})
	case "template":
		return runSandboxTemplate(a, args[1:])
	case "provider":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem sandbox provider list|attach|detach …")
		}
		switch args[1] {
		case "list", "ls":
			if len(args) < 3 {
				return fmt.Errorf("usage: cautem sandbox provider list <sandbox>")
			}
			return a.SandboxProviderList(args[2])
		case "attach":
			if len(args) < 4 {
				return fmt.Errorf("usage: cautem sandbox provider attach <sandbox> <provider>")
			}
			return a.ProviderAttach(args[2], args[3])
		case "detach":
			if len(args) < 4 {
				return fmt.Errorf("usage: cautem sandbox provider detach <sandbox> <provider>")
			}
			return a.ProviderDetach(args[2], args[3])
		default:
			return fmt.Errorf("unknown sandbox provider subcommand %q", args[1])
		}
	default:
		return fmt.Errorf("unknown sandbox subcommand %q", args[0])
	}
}

func runExec(a *service.App, args []string) error {
	parsed, err := osargs.ParseSandboxExec(args)
	if err != nil {
		return err
	}
	env := make([]string, 0, len(parsed.Env))
	for k, v := range parsed.Env {
		env = append(env, k+"="+v)
	}
	return a.Exec(service.ExecOpts{
		Name:    parsed.Name,
		Argv:    parsed.Argv,
		TTY:     term.IsTerminal(int(os.Stdin.Fd())),
		Env:     env,
		WorkDir: parsed.WorkDir,
	})
}

func runSandboxTemplate(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem sandbox template create|list|get|delete …")
	}
	switch args[0] {
	case "create":
		t := templates.Template{}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--name":
				i++
				if i >= len(args) {
					return fmt.Errorf("--name required")
				}
				t.Name = args[i]
			case "--image":
				i++
				if i >= len(args) {
					return fmt.Errorf("--image needs value")
				}
				t.Image = args[i]
			case "--from":
				i++
				if i >= len(args) {
					return fmt.Errorf("--from needs value")
				}
				t.From = args[i]
			case "--policy":
				i++
				if i >= len(args) {
					return fmt.Errorf("--policy needs path")
				}
				t.Policy = args[i]
			case "--cpu":
				i++
				if i >= len(args) {
					return fmt.Errorf("--cpu needs value")
				}
				if _, err := fmt.Sscanf(args[i], "%f", &t.CPU); err != nil {
					return fmt.Errorf("invalid --cpu")
				}
			case "--memory":
				i++
				if i >= len(args) {
					return fmt.Errorf("--memory needs value")
				}
				t.Memory = args[i]
			case "--pids-limit":
				i++
				if i >= len(args) {
					return fmt.Errorf("--pids-limit needs value")
				}
				if _, err := fmt.Sscanf(args[i], "%d", &t.PidsLimit); err != nil || t.PidsLimit == 0 {
					return fmt.Errorf("invalid --pids-limit")
				}
			case "--provider":
				i++
				if i >= len(args) {
					return fmt.Errorf("--provider needs value")
				}
				t.Providers = append(t.Providers, args[i])
			default:
				return fmt.Errorf("unknown flag %q", args[i])
			}
		}
		if t.Name == "" {
			return fmt.Errorf("template create: --name required")
		}
		return a.TemplateCreate(t)
	case "list", "ls":
		return a.TemplateList()
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem sandbox template get <name>")
		}
		return a.TemplateGet(args[1])
	case "delete", "rm":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem sandbox template delete <name>")
		}
		return a.TemplateDelete(args[1])
	default:
		return fmt.Errorf("unknown template subcommand %q", args[0])
	}
}

func runTerm(a *service.App, _ []string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("cautem term: requires an interactive terminal")
	}
	act, err := tuipkg.Run(a)
	if err != nil {
		return err
	}
	switch act.Kind {
	case "connect":
		return a.SandboxConnect(service.ConnectOpts{Name: act.Name})
	case "exec":
		return a.Exec(service.ExecOpts{Name: act.Name, Argv: []string{"bash"}, TTY: true})
	case "logs":
		return a.Logs(act.Name, false)
	default:
		return nil
	}
}

// Main is a helper for tests / embedding.
func Main() {
	if err := Execute(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseProfileIO accepts positional path/id and OpenShell -f/--file, -o/--output.
func parseProfileIO(args []string, fileMode bool) (pathOrID, outFmt string, err error) {
	outFmt = "yaml"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--file", "--url", "--from":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("%s needs a path", args[i-1])
			}
			pathOrID = args[i]
		case "-o", "--output":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("%s needs yaml|json", args[i-1])
			}
			outFmt = strings.ToLower(args[i])
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("unknown flag %q", args[i])
			}
			if pathOrID == "" {
				pathOrID = args[i]
			}
		}
	}
	_ = fileMode
	return pathOrID, outFmt, nil
}

// runSSHProxy is the OpenShell ProxyCommand hook (token / name / legacy modes).
func runSSHProxy(a *service.App, args []string) error {
	parsed, err := osargs.ParseSSHProxy(args)
	if err != nil {
		return err
	}
	return a.SSHProxy(service.SSHProxyOpts{
		GatewayURL:  parsed.GatewayURL,
		GatewayName: parsed.GatewayName,
		SandboxID:   parsed.SandboxID,
		Token:       parsed.Token,
		Name:        parsed.Name,
	})
}

// runProxy is the sidecar / host CONNECT proxy entrypoint.
// Usage: cautem proxy --listen ADDR --policy PATH [--ca-out PATH]
func runProxy(a *service.App, args []string) error {
	opt := service.ProxyOpts{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--listen", "-l":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --listen needs a value")
			}
			i++
			opt.Listen = args[i]
		case "--policy", "-p":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --policy needs a value")
			}
			i++
			opt.Policy = args[i]
		case "--ca-out":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --ca-out needs a value")
			}
			i++
			opt.CAOut = args[i]
		case "--gateway":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --gateway needs a value")
			}
			i++
			opt.GatewayURL = args[i]
		case "--sandbox":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --sandbox needs a value")
			}
			i++
			opt.Sandbox = args[i]
		case "--log-dir":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --log-dir needs a value")
			}
			i++
			opt.LogDir = args[i]
		case "--help", "-h":
			fmt.Fprintln(os.Stderr, "usage: cautem proxy --listen ADDR --policy PATH [--ca-out PATH] [--gateway URL] [--sandbox NAME]")
			return nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("proxy: unknown flag %q", args[i])
			}
			return fmt.Errorf("proxy: unexpected arg %q", args[i])
		}
	}
	if strings.TrimSpace(opt.Policy) == "" {
		return fmt.Errorf("usage: cautem proxy --listen ADDR --policy PATH [--ca-out PATH]")
	}
	return a.Proxy(opt)
}
