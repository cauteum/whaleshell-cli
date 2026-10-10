package osargs_test

import (
	"testing"

	"github.com/cautem/cauteum-cli/internal/osargs"
)

func TestParseGatewayAddOpenShell(t *testing.T) {
	g, err := osargs.ParseGatewayAdd([]string{"http://127.0.0.1:7443", "--local", "--name", "local"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Endpoint != "http://127.0.0.1:7443" || g.Name != "local" || !g.Local {
		t.Fatalf("%+v", g)
	}
}

func TestParseGatewayAddDeriveName(t *testing.T) {
	g, err := osargs.ParseGatewayAdd([]string{"http://127.0.0.1:7443"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "local" || !g.Local {
		t.Fatalf("%+v", g)
	}
}

func TestParseGatewayAddNameURLRejected(t *testing.T) {
	if _, err := osargs.ParseGatewayAdd([]string{"local", "--url", "http://127.0.0.1:7443"}); err == nil {
		t.Fatal("expected error for rejected gateway add NAME --url")
	}
}

func TestParsePolicySet(t *testing.T) {
	p, err := osargs.ParsePolicySet([]string{"demo", "--policy", "/tmp/p.yaml", "--wait"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "demo" || p.Path != "/tmp/p.yaml" || !p.Wait {
		t.Fatalf("%+v", p)
	}
	g, err := osargs.ParsePolicySet([]string{"--global", "--policy", "/tmp/g.yaml", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Global || g.Path != "/tmp/g.yaml" || !g.Yes {
		t.Fatalf("%+v", g)
	}
}

func TestParseForwardStart(t *testing.T) {
	f, err := osargs.ParseForwardStart([]string{"8080", "my-app", "-d"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Port != "8080" || f.Name != "my-app" || !f.Background {
		t.Fatalf("%+v", f)
	}
}

func TestParseServiceExpose(t *testing.T) {
	s, err := osargs.ParseServiceExpose([]string{"my-app", "8080", "web"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Sandbox != "my-app" || s.Port != "8080" || s.Service != "web" {
		t.Fatalf("%+v", s)
	}
}

func TestParseSettingsSet(t *testing.T) {
	s, err := osargs.ParseSettingsSet([]string{"demo", "--key", "ocsf_json_enabled", "--value", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "demo" || s.Key != "ocsf_json_enabled" || s.Value != "true" {
		t.Fatalf("%+v", s)
	}
}

func TestParseSandboxTransfer(t *testing.T) {
	u, err := osargs.ParseSandboxTransfer([]string{"demo", "./src", "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "demo" || u.Path != "./src" || u.Dest != "/workspace" {
		t.Fatalf("%+v", u)
	}
}

func TestParseLogs(t *testing.T) {
	l, err := osargs.ParseLogs([]string{"demo", "--tail", "-n", "50", "--source", "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Name != "demo" || !l.Tail || l.N != 50 || l.Source != "sandbox" {
		t.Fatalf("%+v", l)
	}
}

func TestParseSandboxExec(t *testing.T) {
	e, err := osargs.ParseSandboxExec([]string{"--name", "demo", "--workdir", "/tmp", "--env", "MODE=test", "--", "ls", "-la"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "demo" || e.WorkDir != "/tmp" || e.Env["MODE"] != "test" || len(e.Argv) != 2 {
		t.Fatalf("%+v", e)
	}
	e2, err := osargs.ParseSandboxExec([]string{"demo", "--", "uname", "-a"})
	if err != nil {
		t.Fatal(err)
	}
	if e2.Name != "demo" || len(e2.Argv) != 2 {
		t.Fatalf("%+v", e2)
	}
}
