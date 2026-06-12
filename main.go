package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	// Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	if err := validateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "config invalid: %v\n", err)
		os.Exit(1)
	}

	// Init logger
	initLogger(cfg.Logging.Level, cfg.Timezone())
	logInfo("x-notify-dc starting")

	// Data directory
	dataDir := DataDir(*configPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logError("create data dir: %v", err)
		os.Exit(1)
	}

	// Load watch manager
	watch, err := NewWatchManager(dataDir)
	if err != nil {
		logError("watch manager: %v", err)
		os.Exit(1)
	}
	logInfo("watch list: %d accounts", watch.Count())

	// Load state
	statePath := filepath.Join(dataDir, "state.json")
	state := NewSeenState(statePath)

	// Init X clients
	xClients := make([]*XClient, len(cfg.Twitter.Cookies))
	for i, c := range cfg.Twitter.Cookies {
		xClients[i] = NewXClient(c)
		label := c.Label
		if label == "" {
			label = fmt.Sprintf("cookie-%d", i+1)
		}
		logInfo("x client: %s", label)
	}

	// Init transaction ID generator (non-fatal)
	if err := Init(); err != nil {
		logWarn("transaction ID init failed: %v (continuing without)", err)
	}

	// Init Discord bot
	bot, err := NewDiscordBot(cfg, watch, xClients)
	if err != nil {
		logError("discord bot: %v", err)
		os.Exit(1)
	}

	// Register handlers
	bot.RegisterHandlers()

	// Open Discord connection
	if err := bot.Open(); err != nil {
		logError("discord open: %v", err)
		os.Exit(1)
	}
	logInfo("discord connected")

	// Register slash commands (guild-specific for instant availability, or global)
	guildID := cfg.Discord.GuildID
	if err := bot.RegisterCommands(guildID); err != nil {
		logWarn("register commands: %v", err)
	}
	logInfo("slash commands registered (guild: %s)", guildID)

	// Context with cancel for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Init poller
	poller := NewPoller(cfg, watch, state, xClients, bot)

	// Check if cookie changed while bot was down
	poller.RunResyncCheck()

	// Start poller
	go poller.Run(ctx)

	// Start health checker
	go poller.HealthCheck(ctx)

	// Start config watcher
	go poller.ConfigWatcher(ctx, *configPath)

	logInfo("x-notify-dc online — watching %d accounts, poll interval %s",
		watch.Count(), cfg.Tracking.PollInterval)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logInfo("received %s, shutting down...", sig)
	cancel()
	state.Save()
	bot.Close()
	logInfo("x-notify-dc exited")
}
