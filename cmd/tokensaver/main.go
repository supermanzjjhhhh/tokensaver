package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tokensaver/pkg/gateway"
)

func main() {
	var (
		configPath = flag.String("config", "config/gateway.example.json", "path to gateway JSON config")
		check      = flag.Bool("check", false, "validate config and exit")
	)
	flag.Parse()
	cfg, err := gateway.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	if *check {
		fmt.Printf("config OK: adapter=%s listen=%s mode=%s enabled=%v\n", cfg.Adapter, cfg.Listen, cfg.Core.Mode, cfg.Core.Enabled)
		return
	}
	srv, err := gateway.NewServerWithConfig(*configPath, cfg)
	if err != nil {
		log.Fatalf("server init: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("TokenSaver %s listening on %s adapter=%s mode=%s upstream=%s", gateway.Version, cfg.Listen, cfg.Adapter, cfg.Core.Mode, cfg.Upstream)
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Printf("server error: %v", err)
		os.Exit(1)
	}
	log.Printf("TokenSaver stopped")
}
