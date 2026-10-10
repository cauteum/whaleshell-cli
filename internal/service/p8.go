package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cautem/cautem-cli/internal/storage/gwconfig"
	"github.com/cautem/cautem-driver/driver"
	"github.com/cautem/cautem-sdk/go/cautem"
	"github.com/cautem/slogx"
)

// SandboxStop stops a sandbox without deleting network/volume.
func (a *App) SandboxStop(name string) error {
	const op = "cli.sandbox.stop"
	log := a.op(op, slog.String("sandbox", name))
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		err := fmt.Errorf("sandbox stop: docker not available")
		log.Error("docker unavailable", slogx.Err(err))
		return err
	}
	log.Info("stopping sandbox")
	ctx, cancel := a.withTimeout(TimeoutWait)
	defer cancel()
	info, err := a.Sandboxes.Driver.Inspect(ctx, name)
	if err != nil {
		log.Error("failed to inspect sandbox", slogx.Err(err))
		return err
	}
	if err := a.Sandboxes.Driver.Stop(ctx, info.ID); err != nil {
		log.Error("failed to stop sandbox", slogx.Err(err))
		return err
	}
	a.touchGateway(ctx, info.Name, string(info.ID), info.Image, info.Network, "exited", nil)
	log.Info("sandbox stopped")
	fmt.Printf("sandbox stop: ok %s\n", name)
	return nil
}

// SandboxStart starts a previously stopped sandbox (volume retained).
func (a *App) SandboxStart(name string) error {
	const op = "cli.sandbox.start"
	log := a.op(op, slog.String("sandbox", name))
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		err := fmt.Errorf("sandbox start: docker not available")
		log.Error("docker unavailable", slogx.Err(err))
		return err
	}
	log.Info("starting sandbox")
	ctx, cancel := a.withTimeout(TimeoutWait)
	defer cancel()
	info, err := a.Sandboxes.Driver.Inspect(ctx, name)
	if err != nil {
		log.Error("failed to inspect sandbox", slogx.Err(err))
		return err
	}
	if err := a.Sandboxes.Driver.Start(ctx, info.ID); err != nil {
		log.Error("failed to start sandbox", slogx.Err(err))
		return err
	}
	a.touchGateway(ctx, info.Name, string(info.ID), info.Image, info.Network, "running", nil)
	log.Info("sandbox started")
	fmt.Printf("sandbox start: ok %s\n", name)
	return nil
}

func (a *App) touchGateway(ctx context.Context, name, id, image, network, status string, labels map[string]string) {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return
	}
	u := gwconfig.CurrentURL(cfg)
	if u == "" {
		return
	}
	_ = a.clientFor(u).UpsertSandbox(ctx, cautem.Sandbox{
		Name: name, ID: id, Image: image, Network: network, Status: status, Labels: labels,
	})
}

// Copy transfers files: host→sandbox or sandbox→host.
// Specs look like "localpath" and "name:/path" (sandbox side has a colon).
func (a *App) Copy(src, dst string) error {
	if a.Sandboxes == nil || a.Sandboxes.Driver == nil {
		return fmt.Errorf("cp: docker not available")
	}
	ctx, cancel := a.withTimeout(TimeoutWork)
	defer cancel()
	sName, sPath, sOK := splitSandboxPath(src)
	dName, dPath, dOK := splitSandboxPath(dst)
	switch {
	case !sOK && dOK:
		if err := driver.ValidateUploadDest(dPath); err != nil {
			return fmt.Errorf("cp: %w", err)
		}
		info, err := a.Sandboxes.Driver.Inspect(ctx, dName)
		if err != nil {
			return err
		}
		if err := a.Sandboxes.Driver.CopyTo(ctx, info.ID, src, dPath); err != nil {
			return err
		}
		fmt.Printf("cp: %s → %s:%s\n", src, dName, dPath)
	case sOK && !dOK:
		info, err := a.Sandboxes.Driver.Inspect(ctx, sName)
		if err != nil {
			return err
		}
		if err := a.Sandboxes.Driver.CopyFrom(ctx, info.ID, sPath, dst); err != nil {
			return err
		}
		fmt.Printf("cp: %s:%s → %s\n", sName, sPath, dst)
	default:
		return fmt.Errorf("usage: cautem cp <local> <sandbox>:/path  OR  cautem cp <sandbox>:/path <local>")
	}
	return nil
}

func splitSandboxPath(s string) (name, path string, ok bool) {
	// Avoid treating Windows drive letters; we only support name:/abs
	i := strings.Index(s, ":/")
	if i <= 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}
