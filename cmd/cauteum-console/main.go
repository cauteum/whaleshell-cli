package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cautem/cauteum-cli/internal/console"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "console listen address")
	publicURL := flag.String("public-url", "", "browser-visible console origin (https required off loopback)")
	gateway := flag.String("gateway", "http://127.0.0.1:7443", "Cauteum gateway URL")
	flag.Parse()
	server, err := console.New(console.Config{Listen: *listen, PublicURL: *publicURL, Gateway: *gateway})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("cauteum console listening", "address", *listen, "gateway", *gateway)
	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("cauteum console stopped", "error", err)
		os.Exit(1)
	}
}
