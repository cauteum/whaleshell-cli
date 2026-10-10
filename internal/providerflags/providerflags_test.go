package providerflags_test

import (
	"testing"

	"github.com/cautem/cautem-cli/internal/providerflags"
)

func TestParseCreateOpenShell(t *testing.T) {
	a, err := providerflags.ParseCreate([]string{"--name", "gh", "--type", "github", "--credential", "GITHUB_TOKEN"}, func(k string) (string, bool) {
		if k == "GITHUB_TOKEN" {
			return "tok", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "gh" || a.Profile != "github" || a.Credentials["GITHUB_TOKEN"] != "tok" {
		t.Fatalf("%+v", a)
	}
}

func TestParseCreateFromExisting(t *testing.T) {
	a, err := providerflags.ParseCreate([]string{"--name", "my-claude", "--type", "claude-code", "--from-existing"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "my-claude" || a.Profile != "claude-code" || !a.FromExisting {
		t.Fatalf("%+v", a)
	}
}

func TestParseCreateRejectsPositional(t *testing.T) {
	if _, err := providerflags.ParseCreate([]string{"gh", "--type", "github"}, nil); err == nil {
		t.Fatal("expected error for positional name")
	}
}

func TestParseCreateRequiresType(t *testing.T) {
	if _, err := providerflags.ParseCreate([]string{"--name", "x"}, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseCreateOIDCAndExpiry(t *testing.T) {
	a, err := providerflags.ParseCreate([]string{
		"--name", "gw", "--type", "cursor",
		"--from-oidc-token", "--runtime-credentials",
		"--credential-expires-at", "API_KEY=1700000000000",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.FromOIDCToken || !a.RuntimeCredentials {
		t.Fatalf("%+v", a)
	}
	if a.CredentialExpiresAt["API_KEY"] != 1700000000000 {
		t.Fatalf("expiry %v", a.CredentialExpiresAt)
	}
}

func TestParseRefreshConfigure(t *testing.T) {
	opt, err := providerflags.ParseRefreshConfigure("gh", []string{
		"--credential-key", "GITHUB_TOKEN", "--strategy", "env",
		"--material", "SCOPE=repo", "--credential-expires-at", "999",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opt.CredKey != "GITHUB_TOKEN" || opt.Strategy != "env" || opt.Material["SCOPE"] != "repo" || opt.ExpiresAtMS != 999000 {
		t.Fatalf("%+v", opt)
	}
}
