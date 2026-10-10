// Package app is the composition root for the cauteum CLI binary.
package app

import (
	"context"

	"github.com/cautem/cauteum-cli/internal/app/cli"
	"github.com/cautem/cauteum-cli/internal/logger"
)

// Run initializes logging and executes the CLI.
func Run(ctx context.Context, args []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	log := logger.Setup(ctx, logger.Options{Service: "cauteum"})
	ctx = logger.ToContext(ctx, log)
	return cli.Execute(ctx, args)
}
