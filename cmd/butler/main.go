package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"gitlab.com/sfoerster/butler/internal/config"
	"gitlab.com/sfoerster/butler/internal/proxy"
)

func main() {
	configPath := flag.String("config", "butler.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Info("config loaded",
		"listen", cfg.Listen,
		"upstream", cfg.Upstream,
		"clients", len(cfg.Clients),
	)

	p, err := proxy.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create proxy", "error", err)
		os.Exit(1)
	}

	logger.Info("starting butler", "listen", cfg.Listen)
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           p,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
	if err := server.ListenAndServe(); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
