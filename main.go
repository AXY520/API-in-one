package main

import (
	"api-in-one/config"
	"api-in-one/handler"
	"api-in-one/relay"
	"api-in-one/router"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed web/dist/index.html
var indexHTML []byte

//go:embed web/dist/assets/*
var webAssets embed.FS

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	configPath := "config.yaml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		configPath = p
	}
	if err := config.Load(configPath); err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	handler.InitLogStore(os.Getenv("REQUEST_LOG_PATH"))

	cfg := config.Get()

	// Build channels
	var channels []*relay.Channel
	for _, cc := range cfg.Channels {
		ch := relay.NewChannelFromConfig(cc)
		channels = append(channels, ch)
	}

	pool := relay.NewPool(channels)
	engine := relay.NewEngine(pool)
	r := router.Setup(engine, pool, indexHTML, webAssets)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	slog.Info("starting api-in-one", "addr", addr, "channels", len(channels))

	srv := &http.Server{Addr: addr, Handler: r}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("shutting down", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("server stopped")
}
