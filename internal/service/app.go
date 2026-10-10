// Package app wires concrete runtime / display backends for the CLI.
package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cautem/cauteum-cli/internal/autoprovider"
	"github.com/cautem/cauteum-cli/internal/logger"
	"github.com/cautem/cauteum-cli/internal/osargs"
	"github.com/cautem/cauteum-cli/internal/outfmt"
	"github.com/cautem/cauteum-cli/internal/policywait"
	"github.com/cautem/cauteum-cli/internal/storage/gwconfig"
	"github.com/cautem/cauteum-cli/internal/storage/templates"
	"github.com/cautem/cauteum-cli/internal/ui"
	"github.com/cautem/cauteum-core/defaults"
	"github.com/cautem/cauteum-core/engine"
	"github.com/cautem/cauteum-core/env"
	"github.com/cautem/cauteum-core/policy"
	display "github.com/cautem/cauteum-display"
	"github.com/cautem/cauteum-driver/driver"
	_ "github.com/cautem/cauteum-driver/driver/all"
	"github.com/cautem/cauteum-proxy/proxy"
	"github.com/cautem/cauteum-runtime/inference"
	"github.com/cautem/cauteum-runtime/relayclient"
	"github.com/cautem/cauteum-runtime/sandbox"
	"github.com/cautem/cauteum-runtime/secrets"
	"github.com/cautem/cauteum-sdk/go/cauteum"
	"github.com/cautem/slogx"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// managedSecretKeys tracks keys that must not fall back to host environment
// values after the gateway removes them.
type managedSecretKeys map[string]struct{}

// App holds constructed dependencies for CLI commands.
type App struct {
	Sandboxes  *sandbox.Manager
	Display    display.Stack
	Docker     driver.Engine // Docker/Podman Engine API (nil for vm/k8s stubs)
	DriverName string        // "docker" | "podman" | "vm" | "kubernetes"

	// OpenShell global session (ApplyGlobal).
	OutputFormat        string
	GlobalWorkspace     string
	GatewayURLOverride  string
	GatewayNameOverride string

	// cmdCtx is the per-invocation context (SIGINT/SIGTERM from main).
	cmdCtx   context.Context
	secretMu sync.Mutex
	// gatewaySecretKeys remembers keys that were gateway-managed so a later
	// empty snapshot cannot revive an older value from the process environment.
	gatewaySecretKeys map[string]managedSecretKeys
}

// New builds the default host-side graph.
func New() *App {
	selected, selectionErr := selectedDriver()
	a := &App{Display: display.None{}, DriverName: selected}
	if selectionErr != nil {
		fmt.Fprintf(os.Stderr, "sandbox driver: %v\n", selectionErr)
		a.Sandboxes = &sandbox.Manager{}
		return a
	}
	switch a.DriverName {
	case "vm", "kubernetes":
		d, err := driver.Open(a.DriverName)
		if err != nil {
			a.Sandboxes = &sandbox.Manager{}
			return a
		}
		a.Sandboxes = &sandbox.Manager{Driver: d}
		return a
	}
	var eng driver.Engine
	var err error
	if a.DriverName == "podman" {
		var config map[string]any
		config, err = podmanDriverConfigFromEnvironment()
		if err == nil {
			eng, err = driver.OpenEngineWithConfig(a.DriverName, config)
		}
	} else {
		eng, err = driver.OpenEngine(a.DriverName)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox driver: %v\n", err)
		a.Sandboxes = &sandbox.Manager{}
		return a
	}
	if a.DriverName != "podman" {
		a.DriverName = "docker"
	}
	a.Docker = eng
	a.Sandboxes = &sandbox.Manager{
		Driver: eng,
		Proxy:  proxy.NewCONNECT(&engine.Allowlist{}),
	}
	return a
}

func selectedDriver() (string, error) {
	configured, err := configuredComputeDriversFromEnvironment()
	if err != nil {
		return "", err
	}
	selected := strings.ToLower(strings.TrimSpace(os.Getenv("CAUTEUM_DRIVER")))
	if selected != "" {
		selected = normalizeSelectedDriver(selected)
		if selected != "docker" && selected != "podman" && selected != "vm" && selected != "kubernetes" {
			return "", fmt.Errorf("unsupported CAUTEUM_DRIVER %q", selected)
		}
		if len(configured) > 0 && !slices.Contains(configured, selected) {
			return "", fmt.Errorf("CAUTEUM_DRIVER %q is not selected by openshell.gateway.compute_drivers", selected)
		}
		return selected, nil
	}
	if len(configured) == 0 {
		return "docker", nil
	}
	if len(configured) == 1 {
		return configured[0], nil
	}
	if slices.Contains(configured, "docker") {
		return "docker", nil
	}
	return "", fmt.Errorf("multiple compute drivers are configured; set CAUTEUM_DRIVER to one of: %s", strings.Join(configured, ", "))
}

func normalizeSelectedDriver(selected string) string {
	switch strings.ToLower(strings.TrimSpace(selected)) {
	case "podman":
		return "podman"
	case "vm", "microvm":
		return "vm"
	case "kubernetes", "k8s":
		return "kubernetes"
	default:
		return strings.ToLower(strings.TrimSpace(selected))
	}
}

func envTruthy(key string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// Banner is the short CLI intro.
func (a *App) Banner() string {
	return "cauteum — agent sandbox CLI"
}

// Version reports the CLI stub version.
// BuildVersion is set at release time:
// -ldflags "-X github.com/cautem/cauteum-cli/internal/service.BuildVersion=…".
var BuildVersion = "0.1.0-alpha.1"

func (a *App) Version() string { return "cauteum " + BuildVersion }

// Health probes Docker Engine / Podman API.
func (a *App) Health() error {
	if a.Docker == nil {
		hint := "check DOCKER_HOST / Docker Desktop"
		switch a.DriverName {
		case "podman":
			hint = "check CAUTEUM_PODMAN_SOCKET / podman.socket (systemctl --user start podman.socket)"
		case "vm":
			return fmt.Errorf("health: vm driver is a spike stub (see docs/exp/MICROVM.md)")
		case "kubernetes":
			return fmt.Errorf("health: kubernetes driver is a spike stub (see docs/exp/KUBERNETES.md)")
		}
		return fmt.Errorf("health: %s client unavailable (%s)", a.DriverName, hint)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	p := a.Docker.Health(ctx)
	if !p.OK {
		return fmt.Errorf("health: %s unreachable: %s", a.DriverName, p.Error)
	}
	engine := strings.ToLower(strings.TrimSpace(a.DriverName))
	if engine == "" {
		engine = "compute"
	}
	fmt.Printf("health: ok\n")
	fmt.Printf("  driver:           %s\n", a.DriverName)
	fmt.Printf("  %s.server:  %s\n", engine, p.ServerVersion)
	fmt.Printf("  %s.api:     %s\n", engine, p.APIVersion)
	fmt.Printf("  %s.os:      %s\n", engine, p.OperatingSystem)
	fmt.Printf("  %s.arch:    %s\n", engine, p.Architecture)
	fmt.Printf("  %s.context: %s\n", engine, p.Context)
	fmt.Printf("  isolation:        %s\n", p.Isolation)
	fmt.Printf("  host.goos:        %s\n", p.HostGOOS)
	fmt.Printf("  capabilities:     %s\n", strings.Join(p.Capabilities, ", "))
	if strings.EqualFold(a.DriverName, "podman") {
		fmt.Printf("  podman.rootless:  %s\n", p.Rootless)
		fmt.Printf("  podman.security:  %s\n", strings.Join(p.SecurityOptions, ", "))
	}
	probeCtx, probeCancel := a.withTimeout(TimeoutWait)
	defer probeCancel()
	probe := a.probeLandlock(probeCtx)
	fmt.Printf("  landlock:         %s\n", probe)
	fmt.Printf("  seccomp:          %s\n", driver.SeccompNote())
	for _, img := range []string{defaults.ImageLocal, defaults.ImageCursor} {
		ok := a.Docker.ImagePresent(ctx, img)
		state := "missing (pulled from GHCR on first use)"
		if strings.EqualFold(os.Getenv(defaults.EnvImagePull), "never") {
			state = "missing (task runtime:image:cli / task docker:agent:cursor)"
		}
		if ok {
			state = "present"
		}
		fmt.Printf("  image %-18s %s\n", img+":", state)
	}
	fmt.Printf("  linux helpers:    %s\n", helperStatus())
	if u, err := a.currentGatewayURL(); err == nil && u != "" {
		gctx, gcancel := a.withTimeout(TimeoutAPIShort)
		defer gcancel()
		cli := a.clientFor(u)
		if _, err := cli.Healthz(gctx); err != nil {
			fmt.Printf("  gateway:          unreachable (%s)\n", u)
		} else {
			fmt.Printf("  gateway:          ok (%s)\n", u)
			if info, err := cli.Info(gctx); err == nil {
				printSecretsKEK(info)
			}
		}
	} else {
		fmt.Printf("  gateway:          not selected (cauteum gateway ensure)\n")
	}
	if envTruthy("CAUTEUM_LANDLOCK_REQUIRED") && !landlockABIAtLeast(probe, 1) {
		return fmt.Errorf("health: landlock gate failed (need ABI≥1; got %s). Unset CAUTEUM_LANDLOCK_REQUIRED on Docker Desktop / ABI 0 hosts", probe)
	}
	return nil
}

func printSecretsKEK(info map[string]any) {
	raw, ok := info["secrets_kek"].(map[string]any)
	if !ok {
		return
	}
	src, _ := raw["source"].(string)
	pinned, _ := raw["pinned"].(bool)
	format, _ := raw["format"].(string)
	fmt.Printf("  secrets_kek:      source=%s pinned=%v format=%s\n", src, pinned, format)
	if w, _ := raw["warning"].(string); strings.TrimSpace(w) != "" {
		fmt.Printf("  secrets_kek.warn: %s\n", w)
	}
}

// Doctor is the production Docker-path readiness check (OpenShell doctor alias).
func (a *App) Doctor() error {
	if err := a.Health(); err != nil {
		return err
	}
	probeCtx, probeCancel := a.withTimeout(TimeoutAPIShort)
	probe := a.Docker.Health(probeCtx)
	probeCancel()
	if err := validateDoctorEngine(a.DriverName, probe); err != nil {
		return err
	}
	u, err := a.currentGatewayURL()
	if err != nil || u == "" {
		return fmt.Errorf("doctor: no gateway selected (cauteum gateway ensure|add|select)")
	}
	ctx, cancel := a.withTimeout(TimeoutAPIShort)
	defer cancel()
	cli := a.clientFor(u)
	info, err := cli.Info(ctx)
	if err != nil {
		return fmt.Errorf("doctor: gateway info: %w", err)
	}
	engine := strings.ToLower(strings.TrimSpace(a.DriverName))
	if engine == "" {
		engine = "compute"
	}
	if raw, ok := info["secrets_kek"].(map[string]any); ok {
		if pinned, _ := raw["pinned"].(bool); !pinned {
			fmt.Fprintf(os.Stderr, "doctor: warn: pin %s in compose/env so secrets survive volume loss\n", secrets.EnvKEK)
		}
		if needed, _ := raw["migration_needed"].(bool); needed {
			fmt.Fprintln(os.Stderr, "doctor: warn: legacy secrets store needs migration; back up the data dir and restart gateway")
		}
	}
	if strings.EqualFold(a.DriverName, "docker") {
		fmt.Fprintln(os.Stderr, "doctor: note: binary-scoped egress rules deny callers whose executable cannot be verified; Docker Desktop sidecar TCP usually cannot provide this identity")
	}
	providers, err := cli.ListProviders(ctx)
	if err != nil {
		return fmt.Errorf("doctor: list providers: %w", err)
	}
	fmt.Printf("  providers:        %d instance(s)\n", len(providers))
	for _, p := range providers {
		fmt.Printf("    - %s type=%s env=%v\n", p.Name, p.Type, p.EnvVars)
	}
	fmt.Printf("doctor: ok (%s path)\n", engine)
	return nil
}

func validateDoctorEngine(name string, probe driver.Probe) error {
	if !probe.OK {
		return fmt.Errorf("doctor: %s engine is unavailable: %s", name, probe.Error)
	}
	if !strings.EqualFold(strings.TrimSpace(name), "podman") {
		return nil
	}
	major, ok := leadingVersionNumber(probe.ServerVersion)
	if !ok {
		return fmt.Errorf("doctor: Podman server version %q is not recognizable; run `podman version` and verify the active API socket", probe.ServerVersion)
	}
	if major < 6 {
		return fmt.Errorf("doctor: Podman %s cannot enforce the host-gateway blocking route required by proxy-backed sandboxes; upgrade the selected Podman service to 6 or newer", probe.ServerVersion)
	}
	if !slices.Contains(probe.Capabilities, "libpod-native") || !slices.Contains(probe.Capabilities, "userns") {
		return fmt.Errorf("doctor: Podman API lacks native network/userns capabilities; select the Podman 6 Libpod socket and retry")
	}
	if strings.TrimSpace(probe.Rootless) == "" {
		return fmt.Errorf("doctor: Podman rootless mode could not be determined; check `podman info` and the selected user/system socket")
	}
	return nil
}

func leadingVersionNumber(version string) (int, bool) {
	version = strings.TrimSpace(version)
	start := -1
	for index, char := range version {
		if char >= '0' && char <= '9' {
			start = index
			break
		}
	}
	if start < 0 {
		return 0, false
	}
	major := 0
	for _, char := range version[start:] {
		if char < '0' || char > '9' {
			break
		}
		major = major*10 + int(char-'0')
	}
	return major, major > 0
}

// CleanupDockerTestResources removes only disposable resources created by the
// Testcontainers/Cauteum test lanes. It deliberately does not use
// `docker system prune`: named volumes, running containers and user networks
// are outside this command's scope.
type CleanupReport struct {
	AnonymousVolumes  int  `json:"anonymous_volumes" yaml:"anonymous_volumes"`
	StoppedContainers int  `json:"stopped_containers" yaml:"stopped_containers"`
	DryRun            bool `json:"dry_run" yaml:"dry_run"`
	Removed           bool `json:"removed" yaml:"removed"`
}

func (a *App) CleanupDockerTestResources(ctx context.Context, confirm bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	list := func(args ...string) ([]string, error) {
		out, err := exec.CommandContext(ctx, "docker", args...).Output()
		if err != nil {
			return nil, fmt.Errorf("doctor cleanup: docker %s: %w", strings.Join(args, " "), err)
		}
		var values []string
		for _, line := range strings.Split(string(out), "\n") {
			if value := strings.TrimSpace(line); value != "" {
				values = append(values, value)
			}
		}
		return values, nil
	}
	volumes, err := list("volume", "ls", "-q", "--filter", "label=com.docker.volume.anonymous", "--filter", "dangling=true")
	if err != nil {
		return err
	}
	containers, err := list("ps", "-aq", "--filter", "label=cauteum=1", "--filter", "status=exited")
	if err != nil {
		return err
	}
	report := CleanupReport{AnonymousVolumes: len(volumes), StoppedContainers: len(containers), DryRun: !confirm, Removed: confirm}
	if !confirm {
		return a.emitCleanupReport(report)
	}
	for _, volume := range volumes {
		if out, err := exec.CommandContext(ctx, "docker", "volume", "rm", volume).CombinedOutput(); err != nil {
			return fmt.Errorf("doctor cleanup: remove volume %s: %w (%s)", volume, err, strings.TrimSpace(string(out)))
		}
	}
	for _, container := range containers {
		if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", container).CombinedOutput(); err != nil {
			return fmt.Errorf("doctor cleanup: remove container %s: %w (%s)", container, err, strings.TrimSpace(string(out)))
		}
	}
	return a.emitCleanupReport(report)
}

func (a *App) emitCleanupReport(report CleanupReport) error {
	format := "text"
	if a != nil && a.OutputFormat != "" {
		format = a.OutputFormat
	}
	return outfmt.Emit(os.Stdout, format, func(w io.Writer) error {
		if report.DryRun {
			_, _ = fmt.Fprintf(w, "doctor cleanup: %d anonymous test volume(s), %d stopped test container(s)\n", report.AnonymousVolumes, report.StoppedContainers)
			_, _ = fmt.Fprintln(w, "doctor cleanup: dry-run; repeat with --yes to remove these resources")
			return nil
		}
		_, err := fmt.Fprintln(w, "doctor cleanup: removed scoped test resources")
		return err
	}, report)
}

func landlockABIAtLeast(probe string, min int) bool {
	// probe examples: "abi=2 (probe ok)", "abi=0 (...)"
	const p = "abi="
	i := strings.Index(strings.ToLower(probe), p)
	if i < 0 {
		return false
	}
	rest := probe[i+len(p):]
	n := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n >= min
}

func (a *App) probeLandlock(ctx context.Context) string {
	initBin, err := ensureInitBin(ctx)
	if err != nil {
		return "probe skipped (" + err.Error() + ")"
	}
	if a.Docker == nil {
		return "docker unavailable"
	}
	out, err := a.Docker.RunProbe(ctx, initBin)
	if err != nil {
		return "probe error: " + err.Error()
	}
	return out
}

// PolicyCheck loads and validates a policy YAML file.
func (a *App) PolicyCheck(path string) error {
	const op = "cli.policy.check"
	log := a.op(op, slog.String("path", path))
	log.Info("checking policy")
	doc, err := policy.Load(path)
	if err != nil {
		log.Error("failed to load policy", slogx.Err(err))
		return fmt.Errorf("policy check: %w", err)
	}
	doc, err = a.mergeGatewayGlobal(doc)
	if err != nil {
		log.Error("failed to merge global policy", slogx.Err(err))
		return fmt.Errorf("policy check: %w", err)
	}
	if err := doc.Validate(); err != nil {
		log.Error("policy validation failed", slogx.Err(err))
		return fmt.Errorf("policy check: %w", err)
	}
	var eng engine.Allowlist
	if err := eng.Apply(doc); err != nil {
		log.Error("policy engine apply failed", slogx.Err(err))
		return fmt.Errorf("policy check: engine: %w", err)
	}
	log.Info("policy check ok", slog.Int("allow_rules", len(doc.AllowRules())), slog.String("harden", doc.HardenMode()))
	fmt.Printf("policy check: ok version=%d harden=%s allow_rules=%d include_workdir=%v",
		doc.Version, doc.HardenMode(), len(doc.AllowRules()),
		doc.IncludeWorkdir())
	if doc.Inference != nil && len(doc.Inference.Providers) > 0 {
		fmt.Printf(" inference=%v", doc.Inference.Providers)
	}
	if len(doc.Binaries) > 0 {
		fmt.Printf(" binaries=%d", len(doc.Binaries))
	}
	fmt.Println()
	return nil
}

// PolicyGlobalGet prints gateway global policy YAML.
func (a *App) PolicyGlobalGet() error {
	u, err := a.currentGatewayURL()
	if err != nil {
		return err
	}
	b, err := cauteum.NewWithToken(u, a.gatewayTokenForURL(u)).GetGlobalPolicy(a.apiCtx())
	if err != nil {
		return err
	}
	os.Stdout.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

// PolicyGlobalSet uploads YAML file as gateway global policy.
func (a *App) PolicyGlobalSet(path string) error {
	u, err := a.currentGatewayURL()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc, err := policy.Parse(b)
	if err != nil {
		return err
	}
	if err := doc.Validate(); err != nil {
		return err
	}
	if err := cauteum.NewWithToken(u, a.gatewayTokenForURL(u)).PutGlobalPolicy(a.apiCtx(), b); err != nil {
		return err
	}
	fmt.Printf("policy global set: ok url=%s bytes=%d\n", u, len(b))
	return nil
}

// PolicyGlobalClear removes the gateway-global policy lock.
func (a *App) PolicyGlobalClear() error {
	u, err := a.currentGatewayURL()
	if err != nil {
		return err
	}
	if err := cauteum.NewWithToken(u, a.gatewayTokenForURL(u)).PutGlobalPolicy(a.apiCtx(), nil); err != nil {
		return err
	}
	fmt.Println("policy delete --global: ok")
	return nil
}

// PolicySet stores sandbox base policy; the gateway builds the effective
// candidate via composition (base + provider-composed profiles) — OpenShell-style.
//
// Requires the sandbox to be registered on the current gateway (create/register).
// Without a gateway, falls back to writing the file directly (no composition).
// When wait is true, blocks until the bind file matches and settle elapsed.
func (a *App) PolicySet(sandboxName, path string, wait bool) error {
	const op = "cli.policy.set"
	log := a.op(op, slog.String("sandbox", sandboxName), slog.String("path", path), slog.Bool("wait", wait))
	log.Info("setting sandbox policy")
	b, err := os.ReadFile(path)
	if err != nil {
		log.Error("failed to read policy file", slogx.Err(err))
		return err
	}
	doc, err := policy.Parse(b)
	if err != nil {
		log.Error("failed to parse policy", slogx.Err(err))
		return fmt.Errorf("policy set: %w", err)
	}
	if err := doc.Validate(); err != nil {
		log.Error("policy validation failed", slogx.Err(err))
		return fmt.Errorf("policy set: %w", err)
	}

	var appliedHost string
	var appliedBytes []byte

	if gw, err := a.currentGatewayURL(); err == nil && gw != "" {
		c := cauteum.NewWithToken(gw, a.gatewayTokenForURL(gw))
		ctx, cancel := a.withTimeout(TimeoutAPILong)
		defer cancel()
		if _, err := c.Healthz(ctx); err == nil {
			eff, stripped, err := c.PutSandboxPolicy(ctx, sandboxName, b)
			if err != nil {
				return fmt.Errorf("policy set: %w", err)
			}
			if stripped > 0 {
				fmt.Printf("policy set: stripped %d provider-composed rule(s) from base (prefer policy get --base)\n", stripped)
			}
			effDoc, err := policy.Parse(eff)
			if err != nil {
				return fmt.Errorf("policy set: effective: %w", err)
			}
			var eng engine.Allowlist
			if err := eng.Apply(effDoc); err != nil {
				return fmt.Errorf("policy set: engine: %w", err)
			}
			if a.Docker != nil {
				hostPath, err := a.Docker.PolicyHostPath(ctx, sandboxName)
				if err != nil {
					return fmt.Errorf("policy set: gateway stored base, but live bind: %w (run: cauteum provider effective %s | …)", err, sandboxName)
				}
				if err := writeFileInPlace(hostPath, eff); err != nil {
					return fmt.Errorf("policy set: write %s: %w", hostPath, err)
				}
				appliedHost, appliedBytes = hostPath, eff
				fmt.Printf("policy set: ok sandbox=%s base stored; effective policy applied (rules=%d) host=%s\n",
					sandboxName, len(effDoc.AllowRules()), hostPath)
			} else {
				fmt.Printf("policy set: ok sandbox=%s base stored; effective policy ready (rules=%d, no docker bind)\n",
					sandboxName, len(effDoc.AllowRules()))
			}
			if wait {
				return a.waitPolicy(appliedHost, appliedBytes)
			}
			return nil
		}
	}

	// No gateway: overwrite bind file as-is (no composition).
	fmt.Fprintln(os.Stderr, "policy set: warn: no gateway — writing file directly (no provider composition)")
	doc, err = a.mergeGatewayGlobal(doc)
	if err != nil {
		return fmt.Errorf("policy set: %w", err)
	}
	var eng engine.Allowlist
	if err := eng.Apply(doc); err != nil {
		return fmt.Errorf("policy set: engine: %w", err)
	}
	if a.Docker == nil {
		return fmt.Errorf("policy set: docker not available")
	}
	ctx, cancel := a.withTimeout(TimeoutAPILong)
	defer cancel()
	hostPath, err := a.Docker.PolicyHostPath(ctx, sandboxName)
	if err != nil {
		return err
	}
	if err := writeFileInPlace(hostPath, b); err != nil {
		return fmt.Errorf("policy set: write %s: %w", hostPath, err)
	}
	fmt.Printf("policy set: ok sandbox=%s host=%s allow_rules=%d (proxy reloads within ~1s)\n",
		sandboxName, hostPath, len(doc.AllowRules()))
	if wait {
		return a.waitPolicy(hostPath, b)
	}
	return nil
}

func (a *App) waitPolicy(hostPath string, want []byte) error {
	if hostPath == "" || len(want) == 0 {
		fmt.Println("policy set: --wait skipped (no live bind path)")
		return nil
	}
	ctx, cancel := a.withTimeout(TimeoutPolicyWait)
	defer cancel()
	if err := policywait.FileApplied(ctx, hostPath, want, policywait.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return &ExitError{Code: policywait.ExitCode(err)}
	}
	fmt.Println("policy set: wait ok (file settled)")
	return nil
}

// PolicyUpdate applies incremental network changes to the sandbox base policy.
func (a *App) PolicyUpdate(sandbox string, endpoints, allows, denies, binaries []string, wait bool) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPILong)
	defer cancel()
	baseYAML, err := c.GetSandboxPolicy(ctx, sandbox, "base")
	if err != nil {
		return err
	}
	doc, err := policy.Parse(baseYAML)
	if err != nil {
		return fmt.Errorf("policy update: parse base: %w", err)
	}
	u := policy.NetworkUpdate{Binaries: binaries}
	for _, s := range endpoints {
		ep, err := policy.ParseEndpointSpec(s)
		if err != nil {
			return err
		}
		u.AddEndpoints = append(u.AddEndpoints, ep)
	}
	for _, s := range allows {
		mp, err := policy.ParseMethodPathSpec(s)
		if err != nil {
			return err
		}
		u.AddAllows = append(u.AddAllows, mp)
	}
	for _, s := range denies {
		mp, err := policy.ParseMethodPathSpec(s)
		if err != nil {
			return err
		}
		u.AddDenies = append(u.AddDenies, mp)
	}
	out, err := policy.ApplyNetworkUpdate(doc, u)
	if err != nil {
		return err
	}
	raw, err := yamlMarshalDoc(out)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "cauteum-policy-update-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return a.PolicySet(sandbox, tmpPath, wait)
}

func yamlMarshalDoc(doc policy.Document) ([]byte, error) {
	return yaml.Marshal(doc)
}

// PolicyList prints policy revision metadata.
func (a *App) PolicyList(sandbox string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	revs, err := c.ListPolicyRevisions(a.apiCtx(), sandbox)
	if err != nil {
		return err
	}
	if len(revs) == 0 {
		fmt.Println("policy list: (none)")
		return nil
	}
	for _, r := range revs {
		fmt.Printf("rev=%d status=%s bytes=%d at=%s\n", r.Rev, r.Status, r.Bytes, r.UpdatedAt.Format(time.RFC3339))
	}
	return nil
}

// PolicyGetRevision prints base YAML for a historical revision.
func (a *App) PolicyGetRevision(sandbox string, rev int) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	yamlBody, err := c.GetPolicyRevision(a.apiCtx(), sandbox, rev)
	if err != nil {
		return err
	}
	fmt.Print(yamlBody)
	if len(yamlBody) > 0 && yamlBody[len(yamlBody)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

// SandboxProviderList prints attached provider metadata (no secret values).
func (a *App) SandboxProviderList(sandbox string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	list, err := c.ListSandboxProviders(a.apiCtx(), sandbox)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("sandbox providers: (none)")
		return nil
	}
	for _, p := range list {
		fmt.Printf("%s\ttype=%s\tenv=%v\n", p.Name, p.Type, p.EnvVars)
	}
	return nil
}

// PolicyGet prints sandbox policy YAML. view is "base" or "full" (default full).
func (a *App) PolicyGet(sandboxName, view string) error {
	if view == "" {
		view = "full"
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	b, err := c.GetSandboxPolicy(a.apiCtx(), sandboxName, view)
	if err != nil {
		return err
	}
	fmt.Print(string(b))
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func writeFileInPlace(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func (a *App) mergeGatewayGlobal(doc policy.Document) (policy.Document, error) {
	u, err := a.currentGatewayURL()
	if err != nil || u == "" {
		return doc, nil
	}
	b, err := cauteum.NewWithToken(u, a.gatewayTokenForURL(u)).GetGlobalPolicy(a.apiCtx())
	if err != nil || len(bytesTrim(b)) == 0 {
		return doc, nil
	}
	global, err := policy.Parse(b)
	if err != nil {
		return doc, fmt.Errorf("gateway global policy: %w", err)
	}
	return policy.MergeGlobal(doc, global)
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// InferenceList prints builtin provider presets.
func (a *App) InferenceList() error {
	return inference.ListBuiltins(os.Stdout)
}

// InferenceShow prints effective inference + network allow rules for a policy file.
func (a *App) InferenceShow(path string) error {
	if path == "" {
		path = "policies/default.yaml"
	}
	doc, err := policy.Load(path)
	if err != nil {
		return fmt.Errorf("inference show: %w", err)
	}
	if err := doc.Validate(); err != nil {
		return fmt.Errorf("inference show: %w", err)
	}
	return inference.ShowEffective(os.Stdout, doc)
}

// InferenceLocal prints a host-local model policy snippet.
func (a *App) InferenceLocal() error {
	return inference.WriteLocalSnippet(os.Stdout)
}

// SandboxCreateOpts are CLI flags for sandbox create.
type SandboxCreateOpts struct {
	Name           string
	Image          string
	Workspace      string
	Policy         string
	IKnow          bool
	NoProxy        bool
	NoHarden       bool
	Display        string // none | novnc
	DisplayPort    int
	OpenDisplay    bool
	Labels         map[string]string
	NoHostInternal bool // skip host.cauteum.internal ExtraHosts
	GatewayURL     string
	From           string // BYOC / community image alias
	NoVolume       bool   // skip persist GuestData volume (default: persist)
	GPU            bool
	CDIDevices     []string
	Argv           []string // after `--`: create then exec (OpenShell-like)
	// Providers are profile ids or existing instance names (OpenShell --provider).
	// On create: discover host env, ensure gateway instance, compose policy before start, attach.
	Providers []string
	// AutoProviders enables OpenShell-style inference from trailing argv.
	AutoProviders bool
	// NoAutoProviders forces ModeOff even if AutoProviders is set.
	NoAutoProviders bool
	// Env is non-secret create-time environment (KEY=VALUE). Credential-looking keys warn.
	Env map[string]string
	// NoCredentialWarnings suppresses --env credential-looking key warnings.
	NoCredentialWarnings bool

	Detach    bool
	Editor    string // vscode | cursor
	Forwards  []int
	Upload    string
	CPU       float64
	Memory    string
	PidsLimit int64 // 0 unset (driver default); -1 unlimited; >0 explicit
	Template  string

	DriverConfigJSON string // --driver-config-json
	ApprovalMode     string // manual|auto
	NoKeep           bool   // delete sandbox after main command exits
	ForceTTY         bool   // --tty force PTY for create-time exec

}

// SandboxCreate creates and starts a sandbox.
func (a *App) SandboxCreate(opt SandboxCreateOpts) error {
	const op = "cli.sandbox.create"
	log := a.op(op)
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		err := fmt.Errorf("sandbox create: docker not available")
		log.Error("docker unavailable", slogx.Err(err))
		return err
	}
	if opt.Template != "" {
		tpl, err := templates.Get(opt.Template)
		if err != nil {
			log.Error("failed to load template", slogx.Err(err), slog.String("template", opt.Template))
			return fmt.Errorf("sandbox create template: %w", err)
		}
		mergeTemplateIntoCreate(&opt, tpl)
	}
	applyCreateDefaults(&opt)
	ws := opt.Workspace
	if ws == "" && a.GlobalWorkspace != "" && a.GlobalWorkspace != "default" {
		ws = a.GlobalWorkspace
	}
	if ws == "" {
		ws, _ = os.Getwd()
	}
	name := opt.Name
	if name == "" {
		name = filepath.Base(ws)
	}
	log = log.With(slog.String("sandbox", name), slog.String("workspace", ws))
	log.Info("creating sandbox")
	baseDoc, basePath, err := a.loadOrDenyAll(opt.Policy)
	if err != nil {
		log.Error("failed to load policy", slogx.Err(err))
		return err
	}
	gwURL := opt.GatewayURL
	if gwURL == "" {
		// Production Docker path: gateway is required for secrets + registry unless --no-proxy
		// and no providers (dev escape hatch).
		needGW := !opt.NoProxy || len(opt.Providers) > 0 || opt.AutoProviders
		if err := a.GatewayEnsure(); err != nil {
			if needGW {
				return fmt.Errorf("sandbox create: %w", err)
			}
			fmt.Fprintf(os.Stderr, "sandbox create: warn: %v\n", err)
		}
		if cfg, _, err := gwconfig.Load(); err == nil {
			gwURL = gwconfig.CurrentURL(cfg)
		}
	}
	if gwURL == "" && (!opt.NoProxy || len(opt.Providers) > 0) {
		return fmt.Errorf("sandbox create: gateway required for proxy/providers (cauteum gateway ensure)")
	}
	if !opt.NoCredentialWarnings {
		hints := map[string][]env.ProfileHint{}
		for k := range opt.Env {
			if hs := env.HintKey(k); len(hs) > 0 {
				hints[k] = hs
			}
		}
		env.WarnCredentialEnv(os.Stderr, opt.Env, hints, false)
	}
	mode := autoprovider.ModeOff
	switch {
	case opt.NoAutoProviders:
		mode = autoprovider.ModeOff
	case opt.AutoProviders:
		mode = autoprovider.ModeOn
	default:
		// OpenShell: known trailing agents auto-create providers unless --no-auto-providers.
		if len(InferProvidersFromArgv(opt.Argv)) > 0 {
			mode = autoprovider.ModeOn
		}
	}
	opt.Providers = autoprovider.Merge(opt.Providers, InferProvidersFromArgv(opt.Argv), mode)
	attached, doc, policyPath, err := a.prepareProviders(baseDoc, basePath, opt.Providers, gwURL)
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutWork)
	defer cancel()

	img, err := gwconfig.ResolveImage(opt.From, opt.Image)
	if err != nil {
		return err
	}
	opt.Image = img

	if opt.Labels == nil {
		opt.Labels = map[string]string{}
	}
	if am := strings.ToLower(strings.TrimSpace(opt.ApprovalMode)); am != "" {
		if am != "manual" && am != "auto" {
			return fmt.Errorf("sandbox create: --approval-mode must be manual|auto")
		}
		opt.Labels["cauteum.approval-mode"] = am
	}

	spec := driver.Spec{
		Name:             name,
		Image:            opt.Image,
		Workspace:        ws,
		IKnow:            opt.IKnow,
		Env:              hostEnvForPolicy(doc),
		PolicyPath:       policyPath,
		NoHarden:         opt.NoHarden,
		Labels:           opt.Labels,
		PersistVolume:    !opt.NoVolume,
		GPU:              opt.GPU || envTruthy("CAUTEUM_GPU"),
		CDIDevices:       append([]string{}, opt.CDIDevices...),
		CPU:              opt.CPU,
		PidsLimit:        opt.PidsLimit,
		DriverConfigJSON: opt.DriverConfigJSON,
	}
	if opt.Memory != "" {
		if mem, err := driver.ParseMemoryBytes(opt.Memory); err != nil {
			return fmt.Errorf("sandbox create --memory: %w", err)
		} else {
			spec.MemoryBytes = mem
		}
	}
	for _, p := range opt.Forwards {
		if p > 0 {
			spec.PublishPorts = append(spec.PublishPorts, driver.PortPublish{Host: p, Guest: p})
		}
	}
	for k, v := range opt.Env {
		spec.Env = append(spec.Env, k+"="+v)
	}
	if !opt.NoHostInternal {
		spec.ExtraHosts = driver.HostGatewayExtraHosts()
	}
	spec.GatewayURL = gwURL
	displayURL := ""
	displayPass := ""
	if display.ParseMode(opt.Display) == display.ModeNoVNC ||
		(doc.Display != nil && display.ParseMode(doc.Display.Mode) == display.ModeNoVNC) {
		pass, err := display.RandomPassword()
		if err != nil {
			return err
		}
		port := opt.DisplayPort
		if port <= 0 && doc.Display != nil && doc.Display.Port > 0 {
			port = doc.Display.Port
		}
		if port <= 0 {
			port = display.DefaultPort
		}
		spec.DisplayMode = "novnc"
		spec.DisplayPort = port
		spec.DisplayPassword = pass
		if spec.Image == "" {
			spec.Image = defaults.ImageGUI
		}
		displayPass = pass
		displayURL = display.NoVNCURL("127.0.0.1", port, pass)
	}
	if !opt.NoHarden {
		initBin, err := ensureInitBin(ctx)
		if err != nil {
			return err
		}
		spec.InitBin = initBin
	}
	if !opt.NoProxy {
		bin, err := ensureProxyBin(ctx)
		if err != nil {
			return err
		}
		spec.ProxyBin = bin
		if err := a.attachSupervisor(ctx, &spec, doc, name, gwURL, attached); err != nil {
			return err
		}
	}
	h, err := a.Sandboxes.Create(ctx, sandbox.CreateOptions{
		Spec:   spec,
		Policy: doc,
	})
	if err != nil {
		log.Error("failed to create sandbox", slogx.Err(err))
		return err
	}
	log = log.With(slog.String("sandbox_id", shortID(string(h.ID))), slog.String("image", h.Image))
	if gwURL != "" {
		cli := a.clientFor(gwURL)
		_, profileWorkspace := a.profileScope()
		baseYAML := ""
		if b, err := os.ReadFile(basePath); err == nil {
			baseYAML = string(b)
		}
		_ = cli.UpsertSandbox(ctx, cauteum.Sandbox{
			Name:              h.Name,
			ID:                string(h.ID),
			Image:             h.Image,
			Workspace:         profileWorkspace,
			Network:           h.Network,
			Status:            "running",
			Labels:            opt.Labels,
			BasePolicyYAML:    baseYAML,
			AttachedProviders: attached,
		})
	}
	notes := []string{}
	if !opt.NoProxy {
		notes = append(notes, "proxy=sidecar")
	} else {
		notes = append(notes, "proxy=off")
	}
	if !opt.NoHarden {
		notes = append(notes, "harden=cauteum-init")
	} else {
		notes = append(notes, "harden=off")
	}
	if displayURL != "" {
		notes = append(notes, "display=novnc")
	}
	if spec.GPU {
		notes = append(notes, "gpu=cdi")
	}
	if gwURL != "" {
		notes = append(notes, "gateway=registered")
	}
	if len(opt.Providers) > 0 {
		notes = append(notes, "providers="+strings.Join(opt.Providers, ","))
	}
	log.Info("sandbox created", slog.String("notes", strings.Join(notes, " ")))
	fmt.Printf("sandbox create: ok name=%s id=%s network=%s image=%s %s\n",
		h.Name, shortID(string(h.ID)), h.Network, h.Image, strings.Join(notes, " "))
	ui.Ok("sandbox %s ready (%s)", h.Name, strings.Join(notes, " "))
	if displayURL != "" {
		fmt.Printf("display: %s\n", displayURL)
		fmt.Printf("display password: %s\n", displayPass)
		if opt.OpenDisplay {
			if err := display.OpenHostBrowser(displayURL); err != nil {
				fmt.Fprintf(os.Stderr, "display: open browser: %v\n", err)
			}
		}
	}
	if spec.EnableSSH {
		fmt.Printf("ssh: via gateway relay (cauteum sandbox connect %s | --editor cursor|vscode)\n", h.Name)
	}
	if spec.GPU {
		fmt.Printf("gpu: CDI DeviceRequests enabled (see docs/exp/GPU.md)\n")
	}
	if !opt.NoVolume {
		fmt.Printf("volume: cauteum-data-%s → %s (retained across stop/start)\n", h.Name, defaults.GuestData)
	}
	if opt.Upload != "" {
		dest := "/workspace/" + filepath.Base(opt.Upload)
		if err := driver.ValidateUploadDest(dest); err != nil {
			return fmt.Errorf("sandbox create --upload: %w", err)
		}
		if err := a.Copy(opt.Upload, h.Name+":"+dest); err != nil {
			return err
		}
	}
	if err := a.installAgentConfig(h); err != nil {
		return fmt.Errorf("sandbox create supervisor guidance: %w", err)
	}
	if ed := strings.ToLower(strings.TrimSpace(opt.Editor)); ed != "" {
		if err := a.SandboxConnect(ConnectOpts{Name: h.Name, Editor: ed}); err != nil {
			fmt.Fprintf(os.Stderr, "editor: %v\n", err)
		}
	}
	if len(opt.Argv) > 0 {
		if opt.Detach {
			fmt.Printf("sandbox create: --detach skipping exec of %v\n", opt.Argv)
			return nil
		}
		tty := opt.ForceTTY || term.IsTerminal(int(os.Stdin.Fd()))
		execErr := a.Exec(ExecOpts{
			Name: h.Name,
			Argv: opt.Argv,
			TTY:  tty,
			Env:  hostEnvForPolicy(doc),
		})
		if opt.NoKeep {
			_ = a.SandboxRemove(h.Name)
			fmt.Printf("sandbox create: --no-keep removed %s\n", h.Name)
		}
		return execErr
	}
	return nil
}

// SandboxList prints sandboxes.
type SandboxListOpts struct {
	Limit         uint32
	Offset        uint32
	IDs           bool
	Names         bool
	Selector      string
	AllWorkspaces bool
	Output        string
}

// SandboxList prints gateway-registered sandboxes using OpenShell list semantics.
func (a *App) SandboxList(opt SandboxListOpts) error {
	selector, err := parseSandboxLabelSelector(opt.Selector)
	if err != nil {
		return err
	}
	if opt.Output != "" && opt.Output != "table" && opt.Output != "json" && opt.Output != "yaml" {
		return fmt.Errorf("sandbox list: invalid output %q (expected table, json, or yaml)", opt.Output)
	}
	client, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	list, err := client.ListControlSandboxes(ctx, a.GlobalWorkspace, opt.AllWorkspaces)
	if err != nil {
		return fmt.Errorf("sandbox list: %w", err)
	}
	filtered := list[:0]
	for _, sb := range list {
		if !opt.AllWorkspaces {
			workspace := sb.Workspace
			if workspace == "" {
				workspace = "default"
			}
			selectedWorkspace := a.GlobalWorkspace
			if selectedWorkspace == "" {
				selectedWorkspace = "default"
			}
			if workspace != selectedWorkspace {
				continue
			}
		}
		matched := true
		for key, value := range selector {
			if sb.Labels[key] != value {
				matched = false
				break
			}
		}
		if matched {
			filtered = append(filtered, sb)
		}
	}
	list = filtered
	if opt.Offset >= uint32(len(list)) {
		list = nil
	} else {
		list = list[opt.Offset:]
	}
	if uint32(len(list)) > opt.Limit {
		list = list[:opt.Limit]
	}
	if opt.IDs || opt.Names {
		for _, sb := range list {
			if opt.IDs {
				fmt.Println(sb.ID)
			} else if opt.AllWorkspaces {
				workspace := sb.Workspace
				if workspace == "" {
					workspace = "default"
				}
				fmt.Printf("%s/%s\n", workspace, sb.Name)
			} else {
				fmt.Println(sb.Name)
			}
		}
		return nil
	}
	switch opt.Output {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	case "yaml":
		return yaml.NewEncoder(os.Stdout).Encode(list)
	case "", "table":
	default:
	}
	if len(list) == 0 {
		fmt.Println("sandbox list: (none)")
		return nil
	}
	fmt.Printf("%-16s %-12s %-20s %s\n", "NAME", "ID", "STATUS", "IMAGE")
	for _, s := range list {
		fmt.Printf("%-16s %-12s %-20s %s\n", s.Name, shortID(s.ID), s.Status, s.Image)
	}
	return nil
}

func parseSandboxLabelSelector(raw string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	pairs := 0
	for _, term := range strings.Split(raw, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		pairs++
		if pairs > 64 {
			return nil, fmt.Errorf("sandbox list: label selector exceeds 64 pair limit")
		}
		key, value, ok := strings.Cut(term, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok {
			return nil, fmt.Errorf("sandbox list: invalid selector %q (expected key1=value1,key2=value2)", raw)
		}
		if err := validateSandboxLabelKey(key); err != nil {
			return nil, fmt.Errorf("sandbox list: invalid selector %q: %w", raw, err)
		}
		if err := validateSandboxLabelValue(value); err != nil {
			return nil, fmt.Errorf("sandbox list: invalid selector %q: %w", raw, err)
		}
		// The upstream parser inserts into a map; repeated keys use the last value.
		out[key] = value
	}
	return out, nil
}

func validateSandboxLabelKey(key string) error {
	if key == "" {
		return fmt.Errorf("label key cannot be empty")
	}
	if len(key) > 253 {
		return fmt.Errorf("label key exceeds 253 characters")
	}
	prefix, name, hasPrefix := strings.Cut(key, "/")
	if !hasPrefix {
		name = prefix
		prefix = ""
	}
	if name == "" {
		return fmt.Errorf("label key name segment cannot be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("label key name segment exceeds 63 characters")
	}
	if err := validateLabelSegment(name); err != nil {
		return fmt.Errorf("label key name segment: %w", err)
	}
	if !hasPrefix {
		return nil
	}
	if prefix == "" {
		return fmt.Errorf("label key prefix cannot be empty when '/' is present")
	}
	if len(prefix) > 253 {
		return fmt.Errorf("label key prefix exceeds 253 characters")
	}
	for _, r := range prefix {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return fmt.Errorf("label key prefix must be a DNS subdomain")
		}
	}
	if strings.Contains(prefix, "..") || strings.ContainsAny(prefix[:1]+prefix[len(prefix)-1:], "-.") {
		return fmt.Errorf("label key prefix cannot start or end with '-' or '.', or contain consecutive dots")
	}
	return nil
}

func validateSandboxLabelValue(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 63 {
		return fmt.Errorf("label value exceeds 63 characters")
	}
	return validateLabelSegment(value)
}

func validateLabelSegment(value string) error {
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("contains invalid characters")
		}
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if !unicode.IsLetter(first) && !unicode.IsNumber(first) {
		return fmt.Errorf("must start with alphanumeric character")
	}
	if !unicode.IsLetter(last) && !unicode.IsNumber(last) {
		return fmt.Errorf("must end with alphanumeric character")
	}
	return nil
}

// ListSandboxes returns sandbox infos for TUI / automation.
func (a *App) ListSandboxes() ([]driver.Info, error) {
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		return nil, fmt.Errorf("sandbox list: docker not available")
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	return a.Sandboxes.List(ctx)
}

// SandboxStatus prints one sandbox.
func (a *App) SandboxStatus(nameOrID string) error {
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if gw, err := a.currentGatewayURL(); err == nil && gw != "" {
		sandbox, err := cauteum.NewWithToken(gw, a.gatewayTokenForURL(gw)).GetControlSandbox(ctx, a.GlobalWorkspace, nameOrID)
		if err != nil {
			return fmt.Errorf("sandbox get: %w", err)
		}
		fmt.Printf("name:           %s\n", sandbox.Name)
		fmt.Printf("id:             %s\n", sandbox.ID)
		fmt.Printf("workspace:      %s\n", sandbox.Workspace)
		fmt.Printf("registry_status: %s\n", sandbox.Status)
		fmt.Printf("runtime_status:  unavailable\n")
		fmt.Printf("image:          %s\n", sandbox.Image)
		return nil
	}
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		return fmt.Errorf("sandbox status: docker not available")
	}
	info, err := a.Sandboxes.Driver.Inspect(ctx, nameOrID)
	if err != nil {
		return err
	}
	fmt.Printf("name:    %s\n", info.Name)
	fmt.Printf("id:      %s\n", info.ID)
	fmt.Printf("status:  %s\n", info.Status)
	fmt.Printf("network: %s\n", info.Network)
	fmt.Printf("image:   %s\n", info.Image)
	return nil
}

// SandboxRemove deletes a sandbox.
func (a *App) SandboxRemove(nameOrID string) error {
	const op = "cli.sandbox.remove"
	log := a.op(op, slog.String("sandbox", nameOrID))
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		err := fmt.Errorf("sandbox rm: docker not available")
		log.Error("docker unavailable", slogx.Err(err))
		return err
	}
	log.Info("removing sandbox")
	ctx, cancel := a.withTimeout(TimeoutWait)
	defer cancel()
	name := nameOrID
	if info, err := a.Sandboxes.Driver.Inspect(ctx, nameOrID); err == nil && info.Name != "" {
		name = info.Name
	}
	if err := a.Sandboxes.Remove(ctx, nameOrID); err != nil {
		log.Error("failed to remove sandbox", slogx.Err(err))
		return err
	}
	if cfg, _, err := gwconfig.Load(); err == nil {
		if u := gwconfig.CurrentURL(cfg); u != "" {
			_ = a.clientFor(u).DeleteSandbox(ctx, name)
		}
	}
	log.Info("sandbox removed", slog.String("name", name))
	fmt.Printf("sandbox rm: ok %s\n", nameOrID)
	return nil
}

// LogsOpts configures observation log streaming.
type LogsOpts struct {
	Names  []string
	Follow bool
	All    bool
	Since  string
	Source string
	Level  string
}

// Logs streams sandbox container logs to stdout.
func (a *App) Logs(name string, follow bool) error {
	return a.LogsOpts(LogsOpts{Names: []string{name}, Follow: follow})
}

// LogsOpts streams observation logs (gateway SSE preferred; docker fallback).
func (a *App) LogsOpts(opt LogsOpts) error {
	return a.LogsToOpts(a.CommandContext(), opt, os.Stdout)
}

// LogsTo streams sandbox container logs to w (used by `cauteum term` live observation panel).
func (a *App) LogsTo(ctx context.Context, name string, follow bool, w io.Writer) error {
	return a.LogsToOpts(ctx, LogsOpts{Names: []string{name}, Follow: follow}, w)
}

// LogsToOpts streams one or more sandboxes.
func (a *App) LogsToOpts(ctx context.Context, opt LogsOpts, w io.Writer) error {
	if w == nil {
		w = os.Stdout
	}
	if ctx == nil {
		ctx = a.CommandContext()
	}
	names := opt.Names
	if len(names) == 0 && !opt.All {
		return fmt.Errorf("logs: no sandbox names")
	}
	// Prefer the authorized workspace-scoped control API when a gateway is selected.
	if gw, err := a.currentGatewayURL(); err == nil && gw != "" {
		c := cauteum.NewWithToken(gw, a.gatewayTokenForURL(gw))
		if _, err := c.Healthz(ctx); err == nil {
			var sandboxes []cauteum.Sandbox
			if opt.All {
				sandboxes, err = c.ListControlSandboxes(ctx, "", true)
				if err != nil {
					return fmt.Errorf("logs: list visible sandboxes: %w", err)
				}
			} else {
				workspace := a.GlobalWorkspace
				if workspace == "" {
					workspace = "default"
				}
				sandboxes = make([]cauteum.Sandbox, 0, len(names))
				for _, name := range names {
					sandboxes = append(sandboxes, cauteum.Sandbox{Name: name, Workspace: workspace})
				}
			}
			if opt.Follow {
				return c.WatchControlSandboxSet(ctx, sandboxes, 200, opt.Since, opt.Source, opt.Level, w)
			}
			for _, sandbox := range sandboxes {
				lines, err := c.GetControlSandboxLogsFiltered(ctx, sandbox.Workspace, sandbox.Name, 500, opt.Since, opt.Source, opt.Level)
				if err != nil {
					return fmt.Errorf("logs: workspace %q sandbox %q: %w", sandbox.Workspace, sandbox.Name, err)
				}
				for _, line := range lines {
					prefix := sandbox.Name
					if opt.All {
						prefix = sandbox.Workspace + "/" + sandbox.Name
					}
					if len(sandboxes) == 1 {
						prefix = line.Source
						if prefix == "" {
							prefix = "proxy"
						}
					}
					fmt.Fprintf(w, "[%s] %s\n", prefix, line.Text)
				}
			}
			return nil
		}
	}
	if opt.All {
		list, err := a.ListSandboxes()
		if err != nil {
			return err
		}
		names = make([]string, 0, len(list))
		for _, sandbox := range list {
			names = append(names, sandbox.Name)
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("logs: no sandbox names")
	}
	// Docker fallback.
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		return fmt.Errorf("logs: docker not available")
	}
	if !opt.Follow {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, TimeoutWait)
		defer cancel()
	}
	if len(names) > 1 {
		var mu sync.Mutex
		errCh := make(chan error, len(names))
		for _, n := range names {
			go func() {
				pr, pw := io.Pipe()
				go func() {
					info, err := a.Sandboxes.Driver.Inspect(ctx, n)
					if err != nil {
						_ = pw.CloseWithError(err)
						return
					}
					err = a.Sandboxes.Driver.Logs(ctx, info.ID, opt.Follow, pw)
					_ = pw.CloseWithError(err)
				}()
				sc := bufio.NewScanner(pr)
				for sc.Scan() {
					mu.Lock()
					_, _ = fmt.Fprintf(w, "[%s] %s\n", n, sc.Text())
					mu.Unlock()
				}
				errCh <- sc.Err()
			}()
		}
		var first error
		for range names {
			if err := <-errCh; err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	info, err := a.Sandboxes.Driver.Inspect(ctx, names[0])
	if err != nil {
		return err
	}
	return a.Sandboxes.Driver.Logs(ctx, info.ID, opt.Follow, w)
}

// GatewayAdd registers a named gateway in config.
func (a *App) GatewayAdd(name, url string) error {
	return a.GatewayAddParsed(osargs.GatewayAdd{Name: name, Endpoint: url})
}

// GatewayAddParsed registers a gateway including optional OIDC metadata.
func (a *App) GatewayAddParsed(parsed osargs.GatewayAdd) error {
	name, url := parsed.Name, parsed.Endpoint
	if name == "" || url == "" {
		return fmt.Errorf("usage: cauteum gateway add <endpoint> [--name NAME] [--local] [--oidc-issuer URL]")
	}
	cfg, path, err := gwconfig.Load()
	if err != nil {
		return err
	}
	g := gwconfig.Gateway{
		URL:           url,
		OIDCIssuer:    parsed.OIDCIssuer,
		OIDCClientID:  parsed.OIDCClientID,
		OIDCAudience:  parsed.OIDCAudience,
		OIDCScopes:    parsed.OIDCScopes,
		OIDCAllowHTTP: parsed.OIDCAllowHTTP,
	}
	// Preserve tokens if re-adding same name with same URL.
	if prev, ok := cfg.Gateways[name]; ok && prev.URL == url {
		g.Token = prev.Token
		g.RefreshToken = prev.RefreshToken
		g.TokenExpiresAtMS = prev.TokenExpiresAtMS
		if g.OIDCIssuer == "" {
			g.OIDCIssuer = prev.OIDCIssuer
		}
		if g.OIDCClientID == "" {
			g.OIDCClientID = prev.OIDCClientID
		}
	}
	cfg.Gateways[name] = g
	cfg.Current = name
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("gateway add: ok name=%s url=%s config=%s\n", name, url, path)
	if g.OIDCIssuer != "" {
		fmt.Printf("gateway add: oidc issuer=%s client_id=%s\n", g.OIDCIssuer, g.OIDCClientID)
		fmt.Printf("gateway add: run `cauteum gateway login` for Authorization Code + PKCE\n")
	}
	return nil
}

// GatewaySelect sets the current gateway.
func (a *App) GatewaySelect(name string) error {
	cfg, path, err := gwconfig.Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Gateways[name]; !ok {
		return fmt.Errorf("gateway select: unknown %q", name)
	}
	cfg.Current = name
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("gateway select: %s (%s)\n", name, path)
	return nil
}

// GatewayStatus prints local config + remote /healthz when reachable.
func (a *App) GatewayStatus() error {
	cfg, path, err := gwconfig.Load()
	if err != nil {
		return err
	}
	fmt.Printf("config: %s\n", path)
	fmt.Printf("current: %s\n", cfg.Current)
	for name, g := range cfg.Gateways {
		mark := " "
		if name == cfg.Current {
			mark = "*"
		}
		fmt.Printf("%s %s  %s\n", mark, name, g.URL)
	}
	if u := gwconfig.CurrentURL(cfg); u != "" {
		ctx, cancel := a.withTimeout(TimeoutAPIShort)
		defer cancel()
		info, err := a.clientFor(u).Healthz(ctx)
		if err != nil {
			fmt.Printf("healthz: unreachable (%v)\n", err)
			return nil
		}
		fmt.Printf("healthz: ok %+v\n", info)
	}
	return nil
}

// GatewayListRemote lists sandboxes registered on the current gateway.
func (a *App) GatewayListRemote() error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	u := gwconfig.CurrentURL(cfg)
	if u == "" {
		return fmt.Errorf("gateway list: no current gateway (cauteum gateway add …)")
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	list, err := a.clientFor(u).ListSandboxes(ctx)
	if err != nil {
		return err
	}
	for _, sb := range list {
		fmt.Printf("%s\tid=%s\tstatus=%s\timage=%s\n", sb.Name, shortID(sb.ID), sb.Status, sb.Image)
	}
	if len(list) == 0 {
		fmt.Println("(no sandboxes registered)")
	}
	return nil
}

// ExecOpts for cauteum sandbox exec.
type ExecOpts struct {
	Name    string
	Argv    []string
	TTY     bool
	Env     []string
	WorkDir string
}

// Exec runs a command in a sandbox.
func (a *App) Exec(opt ExecOpts) error {
	const op = "cli.sandbox.exec"
	log := a.op(op, slog.String("sandbox", opt.Name), slog.Int("argv_len", len(opt.Argv)))
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		err := fmt.Errorf("exec: docker not available")
		log.Error("docker unavailable", slogx.Err(err))
		return err
	}
	if opt.Name == "" || len(opt.Argv) == 0 {
		return fmt.Errorf("usage: cauteum sandbox exec [--name] <name> [--workdir DIR] [--env K=V] -- CMD")
	}
	log.Info("executing in sandbox", slog.Bool("tty", opt.TTY))
	// Always overlay credential placeholders from effective policy so attach/refresh
	// works without recreating the container (Docker Config.Env is immutable).
	guestEnv := a.credentialPlaceholdersForSandbox(opt.Name)
	// git ignores SSL_CERT_FILE; older sandboxes lack GIT_SSL_CAINFO at create-time.
	guestEnv = mergeEnvEntries(guestEnv, driver.CABundleEnv(defaults.GuestCAFile))
	guestEnv = mergeEnvEntries(guestEnv, opt.Env)
	tty := opt.TTY
	// Interactive: honor Ctrl+C via command context; no artificial wall clock.
	ctx := a.CommandContext()
	a.emitProc(opt.Name, "LAUNCH", strings.Join(opt.Argv, " "), 0)
	res, err := a.Sandboxes.Exec(ctx, opt.Name, driver.ExecRequest{
		Argv:    opt.Argv,
		TTY:     tty,
		Env:     guestEnv,
		WorkDir: opt.WorkDir,
	})
	code := 0
	if res.ExitCode != 0 {
		code = res.ExitCode
	}
	a.emitProc(opt.Name, "EXIT", strings.Join(opt.Argv, " "), code)
	if err != nil {
		log.Error("exec failed", slogx.Err(err), slog.Int("exit_code", code))
		return err
	}
	if res.ExitCode != 0 {
		log.Info("exec finished with non-zero exit", slog.Int("exit_code", res.ExitCode))
		return &ExitError{Code: res.ExitCode}
	}
	log.Info("exec finished", slog.Int("exit_code", 0))
	return nil
}

func (a *App) emitProc(sandbox, activity, details string, exitCode int) {
	gw, err := a.currentGatewayURL()
	if err != nil || gw == "" || sandbox == "" {
		return
	}
	ts := time.Now().UTC()
	text := fmt.Sprintf("%s OCSF PROC:%s [INFO] ALLOWED %s", ts.Format(time.RFC3339Nano), activity, details)
	if activity == "EXIT" {
		text = fmt.Sprintf("%s OCSF PROC:EXIT [INFO] ALLOWED %s [exit:%d]", ts.Format(time.RFC3339Nano), details, exitCode)
	}
	c := cauteum.NewWithToken(gw, a.gatewayTokenForURL(gw))
	ctx, cancel := a.withTimeout(TimeoutEmit)
	defer cancel()
	_ = c.PostLogs(ctx, sandbox, []cauteum.LogLine{{
		TS: ts, Source: "proc", Level: "INFO", Text: text,
	}})
}

// RunOpts for cauteum run (ensure sandbox + exec).
type RunOpts struct {
	Name        string
	Image       string
	Workspace   string
	Policy      string
	IKnow       bool
	Argv        []string
	NoTTY       bool
	NoProxy     bool
	NoHarden    bool
	Display     string
	DisplayPort int
	OpenDisplay bool
}

// Run ensures a sandbox exists, then execs the command (default: bash).
func (a *App) Run(opt RunOpts) error {
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		return fmt.Errorf("run: docker not available")
	}
	ws := opt.Workspace
	if ws == "" {
		ws, _ = os.Getwd()
	}
	name := opt.Name
	if name == "" {
		name = filepath.Base(ws)
	}
	doc, policyPath, err := a.loadOrDenyAll(opt.Policy)
	if err != nil {
		return err
	}
	guestEnv := hostEnvForPolicy(doc)

	ctx, cancel := a.withTimeout(TimeoutWork)
	defer cancel()
	if _, err := a.Sandboxes.Driver.Inspect(ctx, name); err != nil {
		spec := driver.Spec{
			Name:          name,
			Image:         opt.Image,
			Workspace:     ws,
			IKnow:         opt.IKnow,
			Env:           guestEnv,
			PolicyPath:    policyPath,
			NoHarden:      opt.NoHarden,
			PersistVolume: true,
			ExtraHosts:    driver.HostGatewayExtraHosts(),
		}
		if display.ParseMode(opt.Display) == display.ModeNoVNC ||
			(doc.Display != nil && display.ParseMode(doc.Display.Mode) == display.ModeNoVNC) {
			pass, err := display.RandomPassword()
			if err != nil {
				return err
			}
			port := opt.DisplayPort
			if port <= 0 && doc.Display != nil && doc.Display.Port > 0 {
				port = doc.Display.Port
			}
			if port <= 0 {
				port = display.DefaultPort
			}
			spec.DisplayMode = "novnc"
			spec.DisplayPort = port
			spec.DisplayPassword = pass
			if spec.Image == "" {
				spec.Image = defaults.ImageGUI
			}
			url := display.NoVNCURL("127.0.0.1", port, pass)
			fmt.Fprintf(os.Stderr, "display: %s\n", url)
			fmt.Fprintf(os.Stderr, "display password: %s\n", pass)
			if opt.OpenDisplay {
				_ = display.OpenHostBrowser(url)
			}
		}
		if !opt.NoHarden {
			initBin, err := ensureInitBin(ctx)
			if err != nil {
				return err
			}
			spec.InitBin = initBin
		}
		if !opt.NoProxy {
			bin, err := ensureProxyBin(ctx)
			if err != nil {
				return err
			}
			spec.ProxyBin = bin
			if err := a.attachSupervisor(ctx, &spec, doc, name, ""); err != nil {
				return err
			}
		}
		h, err := a.Sandboxes.Create(ctx, sandbox.CreateOptions{
			Spec:   spec,
			Policy: doc,
		})
		if err != nil {
			return err
		}
		ui.Ok("sandbox %s created", h.Name)
		fmt.Fprintf(os.Stderr, "sandbox create: ok name=%s id=%s\n", h.Name, shortID(string(h.ID)))
	} else {
		info, _ := a.Sandboxes.Driver.Inspect(ctx, name)
		if info.Status != "" && info.Status != "running" {
			_ = a.Sandboxes.Driver.Start(ctx, info.ID)
		}
	}

	argv := opt.Argv
	if len(argv) == 0 {
		argv = ui.DefaultShellArgv()
	}
	tty := false
	if !opt.NoTTY {
		tty = term.IsTerminal(int(os.Stdin.Fd()))
	}
	return a.Exec(ExecOpts{
		Name: name,
		Argv: argv,
		TTY:  tty,
		Env:  guestEnv,
	})
}

// ProxyOpts for foreground host-side CONNECT proxy (debug / host-proxy mode).
type ProxyOpts struct {
	Listen     string
	Policy     string
	CAOut      string // write MITM CA PEM (for clients / sandbox trust)
	GatewayURL string // optional: pull secrets + push OCSF
	Sandbox    string // sandbox name for gateway resolve/push
	LogDir     string // optional daily OCSF file dir (default /var/log)
}

// Proxy runs cauteum-proxy until interrupted.
func (a *App) Proxy(opt ProxyOpts) error {
	const op = "proxy.serve"
	ctx, cancel := a.withCancel()
	defer cancel()
	log := logger.Setup(ctx, logger.Options{Service: "cauteum-proxy"})
	ctx = logger.ToContext(ctx, log)
	log = log.With("op", op)

	// Read the supervisor token once and drop it from the environment before
	// the proxy snapshots os.Environ() into its placeholder secret store.
	sandboxToken := strings.TrimSpace(os.Getenv(EnvSandboxToken))
	_ = os.Unsetenv(EnvSandboxToken)

	listen := opt.Listen
	if listen == "" {
		listen = defaults.ProxyListenLocal()
	}
	doc, _, err := a.loadOrDenyAll(opt.Policy)
	if err != nil {
		return err
	}
	var eng engine.Allowlist
	if err := eng.Apply(doc); err != nil {
		return err
	}
	logDir := opt.LogDir
	if logDir == "" {
		logDir = os.Getenv("CAUTEUM_LOG_DIR")
	}
	if logDir == "" {
		logDir = "/var/log"
	}
	gwURL := opt.GatewayURL
	if gwURL == "" {
		gwURL = os.Getenv("CAUTEUM_GATEWAY_URL")
	}
	sandbox := opt.Sandbox
	if sandbox == "" {
		sandbox = os.Getenv("CAUTEUM_SANDBOX")
	}
	// OpenShell-style: CAUTEUM_GATEWAY_URL must resolve via ExtraHosts
	// (host.cauteum.internal → host-gateway). No hostname guessing.
	var gw *cauteum.Client
	var pusher proxy.LogPusher
	if gwURL != "" && sandbox != "" {
		gwURL = GuestGatewayURL(gwURL)
		gw = cauteum.NewWithToken(gwURL, sandboxToken)
		pusher = gatewayAuditPusher{c: gw}
		if sandboxToken == "" {
			log.Warn("no sandbox supervisor token; gateway calls will be rejected", "env", EnvSandboxToken)
		}
	}
	audit := proxy.NewMultiAudit(os.Stderr, logDir, sandbox, pusher)
	defer audit.Close()
	srv := proxy.NewServer(&eng, audit)
	srv.GatewayToken = sandboxToken
	if gw != nil && sandboxToken != "" {
		policyTLSConfig, tlsErr := relayclient.GatewayTLSConfigFromEnvironment()
		if tlsErr != nil {
			return tlsErr
		}
		srv.ReportPolicyStatus = func(reportCtx context.Context, revision uint32, loadError string) error {
			callCtx, cancel := context.WithTimeout(reportCtx, 5*time.Second)
			defer cancel()
			return relayclient.ReportPolicyStatus(callCtx, relayclient.Config{
				GatewayURL:          gwURL,
				GatewayGRPCEndpoint: strings.TrimSpace(os.Getenv("CAUTEUM_GATEWAY_GRPC_ENDPOINT")),
				Sandbox:             sandbox,
				Token:               sandboxToken,
				TLSConfig:           policyTLSConfig,
			}, revision, loadError)
		}
	}

	// Prefer gateway-stored secrets; process environment remains a fallback only
	// for keys the gateway has never managed for this sandbox.
	if gw != nil {
		if err := a.refreshProxySecrets(srv, gw, sandbox); err != nil {
			log.Warn("gateway secrets unavailable", slogx.Err(err))
		} else {
			log.Info("secrets loaded from gateway", "sandbox", sandbox)
		}
		go func() {
			t := time.NewTicker(credentialRefreshInterval)
			defer t.Stop()
			for range t.C {
				_ = a.refreshProxySecrets(srv, gw, sandbox)
			}
		}()
	}

	// Supervisor relay (OpenShell ConnectSupervisor): outbound session to the
	// gateway bridging SSH sessions to the sandbox sshd socket.
	if sock := strings.TrimSpace(os.Getenv(EnvSSHSocket)); sock != "" && gw != nil && sandboxToken != "" {
		relayTLSConfig, err := relayclient.GatewayTLSConfigFromEnvironment()
		if err != nil {
			return err
		}
		go func() {
			_ = relayclient.Run(ctx, relayclient.Config{
				GatewayURL:              gwURL,
				GatewayGRPCEndpoint:     strings.TrimSpace(os.Getenv("CAUTEUM_GATEWAY_GRPC_ENDPOINT")),
				SupervisorControlSocket: strings.TrimSpace(os.Getenv("CAUTEUM_SUPERVISOR_CONTROL_SOCKET")),
				Sandbox:                 sandbox,
				Token:                   sandboxToken,
				SSHSocket:               sock,
				TargetDialSocket:        strings.TrimSpace(os.Getenv(EnvTargetDialSocket)),
				TLSConfig:               relayTLSConfig,
				Log:                     log.Logger,
			})
		}()
		log.Info("supervisor relay enabled", "socket", sock)
	}

	if ca := srv.CA(); ca != nil && opt.CAOut != "" {
		if err := ca.WriteBundle(opt.CAOut); err != nil {
			return fmt.Errorf("proxy ca-out: %w", err)
		}
		log.Info("wrote mitm ca bundle", "path", opt.CAOut)
	}
	proxy.LifecycleReady(audit, "proxy ready on "+listen)
	log.Info("listening", "addr", listen, "allow_rules", len(doc.AllowRules()), "mitm_ca", srv.CA() != nil)
	if path := strings.TrimSpace(opt.Policy); path != "" {
		go srv.WatchPolicy(ctx, path, time.Second)
		log.Info("watching policy for hot-reload", "path", path)
	}
	return srv.ListenAndServe(ctx, listen)
}

func (a *App) refreshProxySecrets(srv *proxy.Server, c *cauteum.Client, sandbox string) error {
	ctx, cancel := context.WithTimeout(context.Background(), TimeoutAPIShort)
	defer cancel()
	m, err := c.ResolveSecrets(ctx, sandbox)
	if err != nil {
		return err
	}
	a.secretMu.Lock()
	if a.gatewaySecretKeys == nil {
		a.gatewaySecretKeys = make(map[string]managedSecretKeys)
	}
	managed := a.gatewaySecretKeys[sandbox]
	if managed == nil {
		managed = make(managedSecretKeys)
		a.gatewaySecretKeys[sandbox] = managed
	}
	for key := range m {
		managed[key] = struct{}{}
	}
	if m["GITHUB_TOKEN"] != "" {
		managed["GH_TOKEN"] = struct{}{}
	}
	if m["GH_TOKEN"] != "" {
		managed["GITHUB_TOKEN"] = struct{}{}
	}
	store := gatewayProxySecretSnapshot(proxy.LoadSecretsFromEnviron(os.Environ()), m, managed)
	a.secretMu.Unlock()
	srv.SetSecrets(store)
	return nil
}

func gatewayProxySecretSnapshot(environment, gateway map[string]string, managed managedSecretKeys) proxy.SecretStore {
	store := proxy.SecretStore{}
	for key, value := range environment {
		if _, wasGatewayManaged := managed[key]; !wasGatewayManaged {
			store[key] = value
		}
	}
	maps.Copy(store, gateway)
	// Mirror GitHub token aliases (policy/credential_keys may list both).
	if v := store["GITHUB_TOKEN"]; v != "" {
		if _, ok := store["GH_TOKEN"]; !ok {
			store["GH_TOKEN"] = v
		}
	}
	if v := store["GH_TOKEN"]; v != "" {
		if _, ok := store["GITHUB_TOKEN"]; !ok {
			store["GITHUB_TOKEN"] = v
		}
	}
	return store
}

// GuestGatewayURL rewrites loopback (and legacy host.docker.internal) gateway URLs
// to host.cauteum.internal — the OpenShell-style host-gateway alias injected via ExtraHosts.
func GuestGatewayURL(gwURL string) string {
	raw := strings.TrimSpace(gwURL)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "localhost", "::1", "host.docker.internal":
		port := u.Port()
		u.Host = defaults.HostInternal
		if port != "" {
			u.Host = net.JoinHostPort(defaults.HostInternal, port)
		}
	}
	return u.String()
}

// gatewayAuditPusher adapts SDK client to proxy.LogPusher.
type gatewayAuditPusher struct {
	c *cauteum.Client
}

func (g gatewayAuditPusher) PostLogs(ctx context.Context, sandbox string, lines []proxy.AuditLine) error {
	if g.c == nil || len(lines) == 0 {
		return nil
	}
	out := make([]cauteum.LogLine, len(lines))
	for i, l := range lines {
		out[i] = cauteum.LogLine{TS: l.TS, Source: l.Source, Level: l.Level, Text: l.Text}
	}
	return g.c.PostLogs(ctx, sandbox, out)
}

func hostEnvForPolicy(doc policy.Document) []string {
	out := env.FromHostForGuest(doc.CredentialEnvKeys()...)
	// Keep guest agent installs on PATH even when login shells reset it.
	out = append(out,
		"HOME="+defaults.GuestHome,
		"PATH="+defaults.GuestPath,
	)
	return out
}

// credentialPlaceholdersForSandbox returns cauteum:resolve:env placeholders for the
// sandbox effective policy credential keys (and attached provider guest keys).
// Profiles with inject_env: false (Cursor) are skipped — Agent validates the key
// client-side and rejects placeholders.
func (a *App) credentialPlaceholdersForSandbox(sandbox string) []string {
	keys := a.sandboxCredentialKeys(sandbox)
	return env.FromHostForGuest(keys...)
}

func (a *App) sandboxCredentialKeys(sandbox string) []string {
	var keys []string
	seen := map[string]struct{}{}
	omit := map[string]struct{}{} // inject_env:false — never guest-inject
	add := func(list []string) {
		for _, k := range list {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if _, skip := omit[k]; skip {
				continue
			}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPIShort)
		defer cancel()
		if sb, err := c.GetSandbox(ctx, sandbox); err == nil {
			for _, name := range sb.AttachedProviders {
				rec, err := c.GetProvider(ctx, name)
				if err != nil {
					continue
				}
				prof, err := loadBuiltinProfile(rec.Type)
				if err != nil {
					// Unknown profile: fall back to instance env vars.
					add(rec.EnvVars)
					continue
				}
				for _, cred := range prof.Credentials {
					if cred.InjectEnv != nil && !*cred.InjectEnv {
						for _, k := range cred.EnvVars {
							omit[strings.TrimSpace(k)] = struct{}{}
						}
					}
				}
				guest := prof.GuestEnvKeys()
				if len(rec.EnvVars) > 0 {
					// Restrict to instance keys, still honoring inject_env:false.
					var filtered []string
					for _, k := range rec.EnvVars {
						if _, skip := omit[k]; skip {
							continue
						}
						filtered = append(filtered, k)
					}
					guest = filtered
				}
				add(guest)
			}
		}
		if b, err := c.EffectivePolicy(ctx, sandbox); err == nil {
			if doc, err := policy.Parse(b); err == nil {
				add(doc.CredentialEnvKeys())
			}
		}
	}
	return keys
}

func mergeEnvEntries(base, extra []string) []string {
	keys := map[string]int{}
	out := make([]string, 0, len(base)+len(extra))
	add := func(entry string) {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return
		}
		if i, exists := keys[key]; exists {
			out[i] = entry
			return
		}
		keys[key] = len(out)
		out = append(out, entry)
	}
	for _, e := range base {
		add(e)
	}
	for _, e := range extra {
		add(e)
	}
	return out
}

func proxySecretsForPolicy(doc policy.Document) []string {
	out := env.SecretsFromHost(doc.CredentialEnvKeys()...)
	for _, k := range defaults.ProxyEnvKeys {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// EnvSandboxToken carries the sandbox-scoped supervisor token into the proxy
// sidecar only (never the sandbox container).
const EnvSandboxToken = "CAUTEUM_SANDBOX_TOKEN"

// EnvSSHSocket is set by the driver when the sidecar shares the sshd socket.
const EnvSSHSocket = "CAUTEUM_SSH_SOCKET"
const EnvTargetDialSocket = "CAUTEUM_TCP_DIAL_SOCKET"

// proxyGatewayEnv adds CAUTEUM_GATEWAY_URL / CAUTEUM_SANDBOX /
// CAUTEUM_SANDBOX_TOKEN so the sidecar can resolve encrypted provider
// secrets, push OCSF logs and run the supervisor relay (OpenShell-like).
// When an inference route is configured, also inject CAUTEUM_INFERENCE_* for inference.local.
func (a *App) proxyGatewayEnv(sandbox, gwURL, sandboxToken string) []string {
	var out []string
	if gwURL == "" {
		if cfg, _, err := gwconfig.Load(); err == nil {
			gwURL = gwconfig.CurrentURL(cfg)
		}
	}
	if gwURL == "" {
		return out
	}
	guestGW := GuestGatewayURL(gwURL)
	out = append(out, "CAUTEUM_GATEWAY_URL="+guestGW)
	if sandbox != "" {
		out = append(out, "CAUTEUM_SANDBOX="+sandbox)
	}
	if sandboxToken != "" {
		out = append(out, EnvSandboxToken+"="+sandboxToken)
	}
	out = append(out, "CAUTEUM_LOG_DIR=/var/log")
	out = append(out, inferenceProxyEnv(a.clientFor(gwURL))...)
	return out
}

func inferenceProxyEnv(c *cauteum.Client) []string {
	ctx, cancel := context.WithTimeout(context.Background(), TimeoutAPIShort)
	defer cancel()
	route, err := c.GetInference(ctx)
	if err != nil || route.Provider == "" {
		return nil
	}
	out := []string{
		"CAUTEUM_INFERENCE_MODEL=" + route.Model,
		fmt.Sprintf("CAUTEUM_INFERENCE_TIMEOUT=%d", route.TimeoutSec),
	}
	if up := inferenceUpstreamForType(route.Provider, c, ctx); up != "" {
		out = append(out, "CAUTEUM_INFERENCE_UPSTREAM="+up)
	}
	rec, err := c.GetProvider(ctx, route.Provider)
	if err == nil && len(rec.EnvVars) > 0 {
		// Prefer first credential key from host env at create time (sidecar also refreshes via gateway).
		if v, ok := os.LookupEnv(rec.EnvVars[0]); ok && v != "" {
			out = append(out, "CAUTEUM_INFERENCE_API_KEY="+v)
		}
	}
	return out
}

type providerReader interface {
	GetProvider(context.Context, string) (cauteum.ProviderRecord, error)
}

func inferenceUpstreamForType(providerName string, c providerReader, ctx context.Context) string {
	rec, err := c.GetProvider(ctx, providerName)
	if err != nil {
		return ""
	}
	if configured := strings.TrimSpace(rec.Config["base_url"]); configured != "" {
		u, err := url.Parse(configured)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return ""
		}
		return configured
	}
	switch strings.ToLower(rec.Type) {
	case "nvidia":
		return defaults.InferenceNVIDIA
	case "openai", "codex":
		return defaults.InferenceOpenAI
	case "deepinfra":
		return defaults.InferenceDeepInfra
	case "anthropic", "claude", "claude-code":
		return defaults.InferenceAnthropic
	case "ollama":
		return defaults.InferenceOllama
	default:
		return ""
	}
}

func (a *App) loadOrDenyAll(path string) (policy.Document, string, error) {
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return policy.Document{}, "", err
		}
		doc, err := policy.Load(abs)
		if err != nil {
			return policy.Document{}, "", err
		}
		doc, err = a.mergeGatewayGlobal(doc)
		if err != nil {
			return policy.Document{}, "", err
		}
		if err := doc.Validate(); err != nil {
			return policy.Document{}, "", err
		}
		return doc, abs, nil
	}
	dir, err := os.MkdirTemp("", "cauteum-policy-*")
	if err != nil {
		return policy.Document{}, "", err
	}
	abs := filepath.Join(dir, "deny-all.yaml")
	const body = "version: 1\nnetwork:\n  default: deny\n"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		return policy.Document{}, "", err
	}
	doc, err := policy.Parse([]byte(body))
	if err != nil {
		return policy.Document{}, "", err
	}
	return doc, abs, nil
}

// InitOpts for `cauteum init --agent …`.
type InitOpts struct {
	Agent string
	Dir   string
	Force bool
}

// Init writes a starter policy for a known agent.
func (a *App) Init(opt InitOpts) error {
	agent := strings.ToLower(strings.TrimSpace(opt.Agent))
	dir := opt.Dir
	if dir == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	var srcName string
	switch agent {
	case "cursor":
		srcName = "cursor.yaml"
	default:
		return fmt.Errorf("init: unknown agent %q (supported: cursor)", opt.Agent)
	}
	mod, err := findModuleDir("github.com/cautem/cauteum-cli")
	if err != nil {
		return err
	}
	src := filepath.Join(mod, "policies", srcName)
	b, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("init: read bundled policy: %w", err)
	}
	outDir := filepath.Join(absDir, "policies")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(outDir, srcName)
	if _, err := os.Stat(dst); err == nil && !opt.Force {
		return fmt.Errorf("init: %s already exists (use --force)", dst)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("init: wrote %s\n", dst)
	fmt.Printf("next: cauteum policy check %s\n", dst)
	fmt.Printf("      cauteum agent login %s\n", agent)
	return nil
}

// AgentLogin checks host env keys for a named agent (MVP: no OAuth).
func (a *App) AgentLogin(name string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "cursor":
		keys := []string{"CURSOR_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"}
		fmt.Println("agent login cursor: checking host env (no tokens printed)")
		found := 0
		for _, k := range keys {
			if os.Getenv(k) != "" {
				fmt.Printf("  %s: set\n", k)
				found++
			} else {
				fmt.Printf("  %s: missing\n", k)
			}
		}
		if found == 0 {
			return fmt.Errorf("agent login: set at least one of %s", strings.Join(keys, ", "))
		}
		fmt.Println("ok: use --provider cursor (and --provider github if needed) on sandbox create")
		fmt.Println("docs: https://cautem.github.io/cauteum-haven.github.io/guides/cursor/")
		return nil
	default:
		return fmt.Errorf("agent login: unknown agent %q (supported: cursor)", name)
	}
}

func findModuleDir(modulePath string) (string, error) {
	candidates := []string{}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	want := "module " + modulePath
	short := strings.TrimPrefix(modulePath, "github.com/cautem/")
	for _, start := range candidates {
		dir := start
		for range 8 {
			gm := filepath.Join(dir, "go.mod")
			b, err := os.ReadFile(gm)
			if err == nil && strings.Contains(string(b), want) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		try := filepath.Join(start, short)
		if b, err := os.ReadFile(filepath.Join(try, "go.mod")); err == nil &&
			strings.Contains(string(b), want) {
			return try, nil
		}
	}
	return "", fmt.Errorf("cannot find module %s source", modulePath)
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
