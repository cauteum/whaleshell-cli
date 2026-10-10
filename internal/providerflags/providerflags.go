// Package providerflags parses OpenShell provider create argv.
package providerflags

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// CreateArgs is the normalized provider create request.
type CreateArgs struct {
	Name         string
	Profile      string // --type
	FromExisting bool
	EnvVars      []string
	Credentials  map[string]string

	FromOIDCToken       bool
	FromGCloudADC       bool
	RuntimeCredentials  bool
	Config              map[string]string // --config KEY=VALUE
	CredentialExpiresAt map[string]int64  // KEY → epoch ms
}

// ParseCreate accepts OpenShell form only:
//
//	cautem provider create --name NAME --type PROFILE [--from-existing|--credential KEY]
func ParseCreate(args []string, lookupEnv func(string) (string, bool)) (CreateArgs, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	out := CreateArgs{Credentials: map[string]string{}, Config: map[string]string{}}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--name needs a value")
			}
			out.Name = args[i]
		case "--type":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--type needs a value")
			}
			out.Profile = args[i]
		case "--from-existing":
			out.FromExisting = true
		case "--from-oidc-token":
			out.FromOIDCToken = true
		case "--from-gcloud-adc":
			out.FromGCloudADC = true
		case "--runtime-credentials":
			out.RuntimeCredentials = true
		case "--config":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--config needs KEY=VALUE")
			}
			k, v, ok := strings.Cut(args[i], "=")
			if !ok || strings.TrimSpace(k) == "" {
				return CreateArgs{}, fmt.Errorf("--config needs KEY=VALUE")
			}
			out.Config[strings.TrimSpace(k)] = v
		case "--credential-expires-at":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--credential-expires-at needs KEY=TIMESTAMP")
			}
			k, ts, ok := strings.Cut(args[i], "=")
			if !ok {
				return CreateArgs{}, fmt.Errorf("--credential-expires-at needs KEY=TIMESTAMP")
			}
			k = strings.TrimSpace(k)
			ms, err := parseCredentialExpiry(strings.TrimSpace(ts))
			if err != nil {
				return CreateArgs{}, err
			}
			if out.CredentialExpiresAt == nil {
				out.CredentialExpiresAt = map[string]int64{}
			}
			out.CredentialExpiresAt[k] = ms
		case "--env":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--env needs a value")
			}
			for k := range strings.SplitSeq(args[i], ",") {
				k = strings.TrimSpace(k)
				if k != "" {
					out.EnvVars = append(out.EnvVars, k)
				}
			}
		case "--credential":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--credential needs KEY or KEY=VALUE")
			}
			raw := args[i]
			if k, v, ok := strings.Cut(raw, "="); ok {
				k = strings.TrimSpace(k)
				out.Credentials[k] = v
				out.EnvVars = append(out.EnvVars, k)
			} else {
				key := strings.TrimSpace(raw)
				v, ok := lookupEnv(key)
				if !ok || strings.TrimSpace(v) == "" {
					return CreateArgs{}, fmt.Errorf("--credential %s: env var not set on host", key)
				}
				out.Credentials[key] = v
				out.EnvVars = append(out.EnvVars, key)
			}
		default:
			return CreateArgs{}, fmt.Errorf("unknown flag %q (usage: provider create --name NAME --type PROFILE …)", args[i])
		}
	}
	if out.Name == "" {
		return CreateArgs{}, fmt.Errorf("provider create: --name required")
	}
	if out.Profile == "" {
		return CreateArgs{}, fmt.Errorf("provider create: --type required")
	}
	return out, nil
}

func parseCredentialExpiry(raw string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n < 1_000_000_000_000 {
			return n * 1000, nil
		}
		return n, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, fmt.Errorf("timestamp must be epoch ms or RFC3339")
	}
	return t.UTC().UnixMilli(), nil
}

// RefreshConfigureArgs for provider refresh configure.
type RefreshConfigureArgs struct {
	Name        string
	CredKey     string
	Strategy    string
	Material    map[string]string
	ExpiresAtMS int64
}

// ParseRefreshConfigure parses flags after `refresh configure <name>`.
func ParseRefreshConfigure(name string, args []string) (RefreshConfigureArgs, error) {
	out := RefreshConfigureArgs{Name: name, Material: map[string]string{}, Strategy: "env"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--credential-key":
			i++
			if i >= len(args) {
				return RefreshConfigureArgs{}, fmt.Errorf("--credential-key needs a value")
			}
			out.CredKey = args[i]
		case "--strategy":
			i++
			if i >= len(args) {
				return RefreshConfigureArgs{}, fmt.Errorf("--strategy needs a value")
			}
			out.Strategy = args[i]
		case "--material":
			i++
			if i >= len(args) {
				return RefreshConfigureArgs{}, fmt.Errorf("--material needs KEY=VALUE")
			}
			k, v, ok := strings.Cut(args[i], "=")
			if !ok {
				return RefreshConfigureArgs{}, fmt.Errorf("--material needs KEY=VALUE")
			}
			out.Material[strings.TrimSpace(k)] = v
		case "--credential-expires-at":
			i++
			if i >= len(args) {
				return RefreshConfigureArgs{}, fmt.Errorf("--credential-expires-at needs MS or KEY=TS")
			}
			raw := args[i]
			if k, ts, ok := strings.Cut(raw, "="); ok {
				ms, err := parseCredentialExpiry(strings.TrimSpace(ts))
				if err != nil {
					return RefreshConfigureArgs{}, err
				}
				out.ExpiresAtMS = ms
				if out.CredKey == "" {
					out.CredKey = strings.TrimSpace(k)
				}
			} else {
				ms, err := parseCredentialExpiry(raw)
				if err != nil {
					return RefreshConfigureArgs{}, err
				}
				out.ExpiresAtMS = ms
			}
		default:
			return RefreshConfigureArgs{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if out.CredKey == "" {
		return RefreshConfigureArgs{}, fmt.Errorf("--credential-key required")
	}
	return out, nil
}

// ParseRefreshKey parses `refresh rotate|delete <name> --credential-key K`.
func ParseRefreshKey(name string, args []string) (string, error) {
	key := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--credential-key":
			i++
			if i >= len(args) {
				return "", fmt.Errorf("--credential-key needs a value")
			}
			key = args[i]
		default:
			return "", fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if key == "" {
		return "", fmt.Errorf("--credential-key required")
	}
	return key, nil
}
