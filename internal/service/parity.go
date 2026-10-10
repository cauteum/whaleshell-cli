package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cautem/cauteum-cli/internal/outfmt"
	"github.com/cautem/cauteum-cli/internal/providerflags"
	"github.com/cautem/cauteum-cli/internal/storage/gwconfig"
	"github.com/cautem/cauteum-core/defaults"
	"github.com/cautem/cauteum-runtime/refresh"
	"github.com/cautem/cauteum-sdk/go/cauteum"
)

// ProviderGet prints provider metadata (no secret values).
func (a *App) ProviderGet(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	rec, err := c.GetProvider(a.apiCtx(), name)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(rec, "", "  ")
	fmt.Println(string(b))
	return nil
}

// ProviderDelete removes a provider instance and its encrypted credentials.
func (a *App) ProviderDelete(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	if err := c.DeleteProvider(a.apiCtx(), name); err != nil {
		return err
	}
	fmt.Printf("deleted provider %s\n", name)
	return nil
}

// ProviderProfileDelete removes a custom gateway profile.
func (a *App) ProviderProfileDelete(id string, scopeOptions ...string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	scope, workspace := a.profileScope(scopeOptions...)
	if err := c.DeleteProfileScoped(a.apiCtx(), id, scope, workspace); err != nil {
		return err
	}
	fmt.Printf("deleted profile %s\n", id)
	return nil
}

// ProviderRefresh re-injects credential values from the host env into the gateway store.
func (a *App) ProviderRefresh(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	rec, err := c.GetProvider(a.apiCtx(), name)
	if err != nil {
		return err
	}
	keys := rec.EnvVars
	if len(keys) == 0 {
		return fmt.Errorf("provider %s has no env_vars to refresh", name)
	}
	results, err := (refresh.FromEnv{}).Refresh(a.apiCtx(), keys)
	creds := map[string]string{}
	for _, r := range results {
		creds[r.Key] = r.Value
	}
	if len(creds) == 0 {
		return err
	}
	rec.Credentials = creds
	// Provider GET redacts refresh material. Preserve the gateway's encrypted
	// refresh configuration by omitting it from this credential-only update.
	rec.Refresh = nil
	if putErr := c.PutProvider(a.apiCtx(), rec); putErr != nil {
		return putErr
	}
	fmt.Printf("refreshed provider %s (%d keys from host env)\n", name, len(creds))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: %v\n", err)
	}
	return nil
}

// ProviderRefreshStatus reports whether host env keys for a provider are set (not values).
func (a *App) ProviderRefreshStatus(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	rec, err := c.GetProvider(a.apiCtx(), name)
	if err != nil {
		return err
	}
	for _, k := range rec.EnvVars {
		_, ok := os.LookupEnv(k)
		state := "missing"
		if ok {
			state = "set"
		}
		fmt.Printf("%s\t%s\n", k, state)
	}
	return nil
}

// GatewayRemove drops a named gateway from local CLI config.
func (a *App) GatewayRemove(name string) error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	if cfg.Gateways == nil {
		return fmt.Errorf("no gateways configured")
	}
	if _, ok := cfg.Gateways[name]; !ok {
		return fmt.Errorf("gateway %q not found", name)
	}
	delete(cfg.Gateways, name)
	if cfg.Current == name {
		cfg.Current = ""
	}
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("removed gateway %s\n", name)
	return nil
}

// GatewayInfo prints the selected gateway's caller-visible control summary.
func (a *App) GatewayInfo() error {
	if err := a.GatewayEnsure(); err != nil {
		return err
	}
	u, err := a.currentGatewayURL()
	if err != nil {
		return err
	}
	info, err := cauteum.NewWithToken(u, a.gatewayTokenForURL(u)).ControlOverview(a.apiCtx())
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	fmt.Println(string(b))
	return nil
}

// GatewayLogin stores a bearer token for the current gateway (local config).
func (a *App) GatewayLogin(token string) error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	if cfg.Current == "" {
		return fmt.Errorf("no gateway selected")
	}
	g := cfg.Gateways[cfg.Current]
	g.Token = strings.TrimSpace(token)
	cfg.Gateways[cfg.Current] = g
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("logged in to gateway %s\n", cfg.Current)
	return nil
}

// GatewayLogout clears the stored token.
func (a *App) GatewayLogout() error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	if cfg.Current == "" {
		return fmt.Errorf("no gateway selected")
	}
	g := cfg.Gateways[cfg.Current]
	g.Token = ""
	g.RefreshToken = ""
	g.TokenExpiresAtMS = 0
	cfg.Gateways[cfg.Current] = g
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("logged out of gateway %s\n", cfg.Current)
	return nil
}

// WhoamiSnapshot is structured identity for -o json|yaml.
type WhoamiSnapshot struct {
	Gateway   string   `json:"gateway"`
	URL       string   `json:"url,omitempty"`
	Auth      string   `json:"auth"`
	Subject   string   `json:"subject,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	IdP       string   `json:"idp,omitempty"`
	GatewayID string   `json:"gateway_id,omitempty"`
}

// Whoami prints gateway identity + selected gateway name.
func (a *App) Whoami() error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	snap := WhoamiSnapshot{
		Gateway: cfg.Current,
		Auth:    "anonymous",
		Subject: "anonymous",
	}
	if cfg.Current != "" {
		g := cfg.Gateways[cfg.Current]
		snap.URL = g.URL
		if strings.TrimSpace(g.Token) != "" {
			snap.Auth = "token"
		}
		cli := cauteum.NewWithToken(g.URL, g.Token)
		ctx, cancel := a.withTimeout(TimeoutAPIShort)
		defer cancel()
		if who, err := cli.Whoami(ctx); err == nil {
			if s, ok := who["subject"].(string); ok && s != "" {
				snap.Subject = s
			}
			if auth, ok := who["auth"].(string); ok && auth != "" {
				snap.Auth = auth
			}
			if id, ok := who["gateway_id"].(string); ok {
				snap.GatewayID = id
			}
			if idp, ok := who["idp"].(string); ok {
				snap.IdP = idp
			}
			if roles, ok := who["roles"].([]any); ok {
				for _, r := range roles {
					if s, ok := r.(string); ok {
						snap.Roles = append(snap.Roles, s)
					}
				}
			}
		} else if info, err := cli.Info(ctx); err == nil {
			if id, ok := info["gateway_id"].(string); ok {
				snap.GatewayID = id
			}
		}
	}
	format := "text"
	if a != nil && a.OutputFormat != "" {
		format = a.OutputFormat
	}
	return outfmt.Emit(os.Stdout, format, func(w io.Writer) error {
		fmt.Fprintf(w, "gateway: %s\n", snap.Gateway)
		if snap.URL != "" {
			fmt.Fprintf(w, "url: %s\n", snap.URL)
		}
		fmt.Fprintf(w, "auth: %s\n", snap.Auth)
		if snap.Subject != "" {
			fmt.Fprintf(w, "subject: %s\n", snap.Subject)
		}
		if snap.GatewayID != "" {
			fmt.Fprintf(w, "gateway_id: %s\n", snap.GatewayID)
		}
		if snap.IdP != "" {
			fmt.Fprintf(w, "idp: %s\n", snap.IdP)
		}
		if len(snap.Roles) > 0 {
			fmt.Fprintf(w, "roles: %s\n", strings.Join(snap.Roles, ","))
		}
		return nil
	}, snap)
}

// localParityDir is ~/.config/cauteum/parity for workspace/settings/forward/service/rule MVP state.
func localParityDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "cauteum", "parity")
	return dir, os.MkdirAll(dir, 0o755)
}

func readJSONMap(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func writeJSONMap(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// WorkspaceCreate registers a named workspace on the gateway (local fallback).
func (a *App) WorkspaceCreate(name string) error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if _, err := c.Healthz(ctx); err == nil {
			if _, err := c.CreateWorkspace(ctx, name); err != nil {
				return err
			}
			fmt.Printf("workspace %s created\n", name)
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "workspaces.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	m[name] = map[string]any{"name": name, "created_at": time.Now().UTC().Format(time.RFC3339), "members": []any{}}
	if err := writeJSONMap(path, m); err != nil {
		return err
	}
	fmt.Printf("workspace %s created (local)\n", name)
	return nil
}

func (a *App) WorkspaceList() error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if list, err := c.ListWorkspaces(ctx); err == nil {
			for _, ws := range list {
				fmt.Println(ws.Name)
			}
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	m, err := readJSONMap(filepath.Join(dir, "workspaces.json"))
	if err != nil {
		return err
	}
	for k := range m {
		fmt.Println(k)
	}
	return nil
}

func (a *App) WorkspaceGet(name string) error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if ws, err := c.GetWorkspace(ctx, name); err == nil {
			b, _ := json.MarshalIndent(ws, "", "  ")
			fmt.Println(string(b))
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	m, err := readJSONMap(filepath.Join(dir, "workspaces.json"))
	if err != nil {
		return err
	}
	v, ok := m[name]
	if !ok {
		return fmt.Errorf("workspace %q not found", name)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
	return nil
}

func (a *App) WorkspaceDelete(name string) error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if err := c.DeleteWorkspace(ctx, name); err == nil {
			fmt.Printf("workspace %s deleted\n", name)
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "workspaces.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	if _, ok := m[name]; !ok {
		return fmt.Errorf("workspace %q not found", name)
	}
	delete(m, name)
	if err := writeJSONMap(path, m); err != nil {
		return err
	}
	fmt.Printf("workspace %s deleted\n", name)
	return nil
}

func (a *App) WorkspaceMemberAdd(ws, member string) error {
	return a.WorkspaceMemberAddRole(ws, member, "user")
}

func (a *App) WorkspaceMemberAddRole(ws, member, role string) error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if err := c.WorkspaceMemberAdd(ctx, ws, member, role); err == nil {
			fmt.Printf("workspace member add: %s → %s role=%s\n", member, ws, role)
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "workspaces.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	raw, _ := m[ws].(map[string]any)
	if raw == nil {
		raw = map[string]any{"name": ws, "members": map[string]any{}}
	}
	members, _ := raw["members"].(map[string]any)
	if members == nil {
		members = map[string]any{}
	}
	members[member] = map[string]any{"subject": member, "role": role}
	raw["members"] = members
	m[ws] = raw
	if err := writeJSONMap(path, m); err != nil {
		return err
	}
	fmt.Printf("workspace member add: %s → %s role=%s (local)\n", member, ws, role)
	return nil
}

func (a *App) WorkspaceMemberRemove(ws, member string) error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if err := c.WorkspaceMemberRemove(ctx, ws, member); err == nil {
			fmt.Printf("workspace member remove: %s ← %s\n", member, ws)
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "workspaces.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	raw, ok := m[ws].(map[string]any)
	if !ok {
		return fmt.Errorf("workspace %q not found", ws)
	}
	members, _ := raw["members"].(map[string]any)
	delete(members, member)
	raw["members"] = members
	m[ws] = raw
	return writeJSONMap(path, m)
}

func (a *App) WorkspaceMemberList(ws string) error {
	return a.WorkspaceGet(ws)
}

func (a *App) SettingsGet(key string) error {
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	m, err := readJSONMap(filepath.Join(dir, "settings.json"))
	if err != nil {
		return err
	}
	if key == "" {
		b, _ := json.MarshalIndent(m, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	v, ok := m[key]
	if !ok {
		return fmt.Errorf("setting %q not found", key)
	}
	fmt.Printf("%v\n", v)
	return nil
}

func (a *App) SettingsSet(key, value string) error {
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	m[key] = value
	if err := writeJSONMap(path, m); err != nil {
		return err
	}
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if _, err := c.Healthz(ctx); err == nil {
			if err := c.PutSetting(ctx, key, value); err != nil {
				fmt.Fprintf(os.Stderr, "settings: gateway PutSetting: %v (local file updated)\n", err)
			}
		}
	}
	fmt.Printf("settings set: %s=%s\n", key, value)
	return nil
}

func (a *App) SettingsDelete(key string) error {
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	delete(m, key)
	return writeJSONMap(path, m)
}

func (a *App) ServiceExpose(sandbox, name, port string) error {
	guestPort, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || guestPort <= 0 {
		return fmt.Errorf("invalid port %q", port)
	}
	if name == "" {
		name = sandbox
	}
	backendHost := "127.0.0.1"
	backendPort := guestPort
	if a.Docker != nil && a.Sandboxes != nil && a.Sandboxes.Driver != nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if info, err := a.Sandboxes.Driver.Inspect(ctx, sandbox); err == nil {
			if ip, err := a.Docker.ContainerIP(ctx, string(info.ID), info.Network); err == nil && ip != "" {
				backendHost = ip
				backendPort = guestPort
			}
		}
	}
	gwPort := defaults.GatewayPort
	edgeURL := fmt.Sprintf("http://%s.openshell.localhost:%d/", name, gwPort)
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if u, err := a.currentGatewayURL(); err == nil {
			if p := gatewayURLPort(u); p > 0 {
				gwPort = p
				edgeURL = fmt.Sprintf("http://%s.openshell.localhost:%d/", name, gwPort)
			}
		}
		if _, err := c.ExposeService(ctx, sandbox, name, uint32(guestPort)); err == nil {
			fmt.Printf("service %s exposed on %s:%d\n", name, sandbox, guestPort)
			fmt.Printf("  %s\n", edgeURL)
			fmt.Printf("  http://%s.cauteum.localhost:%d/\n", name, gwPort)
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "services.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	m[name] = map[string]any{
		"sandbox": sandbox, "port": guestPort, "name": name,
		"backend_host": backendHost, "backend_port": backendPort,
	}
	if err := writeJSONMap(path, m); err != nil {
		return err
	}
	fmt.Printf("service %s exposed on %s:%d (local registry)\n", name, sandbox, guestPort)
	fmt.Printf("  %s\n", edgeURL)
	return nil
}

func gatewayURLPort(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	if u.Port() != "" {
		p, _ := strconv.Atoi(u.Port())
		return p
	}
	switch u.Scheme {
	case "https":
		return 443
	case "http":
		return 80
	}
	return 0
}

// ProviderRefreshConfigure sets gateway refresh strategy for a credential key.
func (a *App) ProviderRefreshConfigure(opt providerflags.RefreshConfigureArgs) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.ConfigureProviderRefresh(ctx, opt.Name, opt.CredKey, opt.Strategy, opt.Material, opt.ExpiresAtMS); err != nil {
		return err
	}
	fmt.Printf("provider refresh configured: %s key=%s strategy=%s\n", opt.Name, opt.CredKey, opt.Strategy)
	return nil
}

// ProviderRefreshRotate triggers credential rotation on the gateway host env.
func (a *App) ProviderRefreshRotate(name, key string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.RotateProviderRefresh(ctx, name, key); err != nil {
		return err
	}
	fmt.Printf("provider refresh rotate: %s key=%s\n", name, key)
	return nil
}

// ProviderRefreshDelete removes refresh configuration for a credential key.
func (a *App) ProviderRefreshDelete(name, key string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.DeleteProviderRefresh(ctx, name, key); err != nil {
		return err
	}
	fmt.Printf("provider refresh delete: %s key=%s\n", name, key)
	return nil
}

func (a *App) ServiceList() error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if list, err := c.ListServices(ctx); err == nil {
			b, _ := json.MarshalIndent(list, "", "  ")
			fmt.Println(string(b))
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	m, err := readJSONMap(filepath.Join(dir, "services.json"))
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(b))
	return nil
}

func (a *App) ServiceGet(name string) error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if rec, err := c.GetService(ctx, name); err == nil {
			b, _ := json.MarshalIndent(rec, "", "  ")
			fmt.Println(string(b))
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	m, err := readJSONMap(filepath.Join(dir, "services.json"))
	if err != nil {
		return err
	}
	v, ok := m[name]
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
	return nil
}

func (a *App) ServiceDelete(name string) error {
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPI)
		defer cancel()
		if err := c.DeleteService(ctx, name); err == nil {
			fmt.Printf("service %s deleted\n", name)
			return nil
		}
	}
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "services.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	delete(m, name)
	return writeJSONMap(path, m)
}

func (a *App) RuleList() error {
	return a.RuleListFilter("", "")
}

func (a *App) RuleReject(id string) error {
	return a.RuleRejectReason(id, "")
}

func (a *App) ruleSet(id, state, reason string) error {
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "rules.json")
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	raw, _ := m[id].(map[string]any)
	if raw == nil {
		raw = map[string]any{"id": id}
	}
	raw["state"] = state
	raw["status"] = state
	raw["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	if reason != "" {
		raw["reason"] = reason
	}
	m[id] = raw
	return writeJSONMap(path, m)
}

func (a *App) RuleClear() error {
	dir, err := localParityDir()
	if err != nil {
		return err
	}
	return writeJSONMap(filepath.Join(dir, "rules.json"), map[string]any{})
}
