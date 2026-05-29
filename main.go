package main

import (
	"api-in-one/config"
	"api-in-one/model"
	"api-in-one/relay"
	"api-in-one/router"
	"fmt"
	"log/slog"
	"os"
)

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

	cfg := config.Get()

	// Build channels
	var channels []*model.Channel
	for _, cc := range cfg.Channels {
		ch := model.NewChannel(cc.Name, cc.Type, cc.BaseURL, cc.BaseURLClaude, cc.BaseURLGemini, cc.Keys, cc.Models, cc.ModelMapping, cc.Priority, cc.Weight)
		if !cc.Enabled {
			ch.Enabled = false
		}
		channels = append(channels, ch)
	}

	pool := relay.NewPool(channels)
	engine := relay.NewEngine(pool)
	r := router.Setup(engine, pool)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	slog.Info("starting api-in-one", "addr", addr, "channels", len(channels))
	if err := r.Run(addr); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
