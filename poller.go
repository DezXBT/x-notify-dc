package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Poller polls X for new tweets from watched accounts and sends Discord notifications.
type Poller struct {
	cfg      *Config
	watch    *WatchManager
	state    *SeenState
	clients  []*XClient
	bot      *DiscordBot
	clientIdx int
}

func NewPoller(cfg *Config, watch *WatchManager, state *SeenState, clients []*XClient, bot *DiscordBot) *Poller {
	return &Poller{
		cfg:     cfg,
		watch:   watch,
		state:   state,
		clients: clients,
		bot:     bot,
	}
}

func (p *Poller) nextClient() *XClient {
	c := p.clients[p.clientIdx]
	p.clientIdx = (p.clientIdx + 1) % len(p.clients)
	return c
}

// Run starts the polling loop until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	interval := p.cfg.PollIntervalDuration()
	logInfo("[poller] starting with interval %s", interval)

	// First run: seed all accounts silently (warmup)
	p.warmup()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logInfo("[poller] stopping")
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logError("[poller] recovered from panic: %v", r)
						pollStats.TotalErrors++
					}
				}()
				p.scanOnce()
			}()
		}
	}
}

// warmup seeds the last tweet ID for all watched accounts without sending alerts.
func (p *Poller) warmup() {
	entries := p.watch.GetAll()
	for _, entry := range entries {
		// Skip if already has baseline
		if p.state.GetLastTweetID(strings.ToLower(entry.Handle)) != "" {
			continue
		}

		if entry.UserID == "" {
			// Need to resolve user ID first
			xc := p.nextClient()
			user, err := xc.GetUser(entry.Handle)
			if err != nil {
				logWarn("[warmup] resolve @%s: %v", entry.Handle, err)
				continue
			}
			p.watch.UpdateUserDetails(entry.Handle, user.RestID, user.FollowersCount, user.ProfileImageURL, user.Name)
			entry.UserID = user.RestID
		}

		xc := p.nextClient()
		tweets, err := xc.GetUserTweets(entry.UserID, 1)
		if err != nil {
			logWarn("[warmup] @%s fetch tweets: %v", entry.Handle, err)
			continue
		}
		if len(tweets) > 0 {
			p.state.SetLastTweetID(strings.ToLower(entry.Handle), tweets[0].ID)
			logInfo("[warmup] @%s baseline: %s", entry.Handle, tweets[0].ID)
		}
		time.Sleep(300 * time.Millisecond)
	}
	p.state.Save()
}

// scanOnce checks all watched accounts for new tweets.
func (p *Poller) scanOnce() {
	started := time.Now()
	pollStats.TotalPolls++
	pollStats.LastPollTime = started

	entries := p.watch.GetAll()
	if len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		logDebug("[poll] checking @%s", entry.Handle)

		if entry.UserID == "" {
			// Resolve user ID
			xc := p.nextClient()
			user, err := xc.GetUser(entry.Handle)
			if err != nil {
				logWarn("[poll] resolve @%s: %v", entry.Handle, err)
				pollStats.TotalErrors++
				continue
			}
			p.watch.UpdateUserDetails(entry.Handle, user.RestID, user.FollowersCount, user.ProfileImageURL, user.Name)
			entry.UserID = user.RestID
		}

		xc := p.nextClient()
		tweets, err := xc.GetUserTweets(entry.UserID, p.cfg.Tracking.TweetsPerCheck)
		if err != nil {
			if isAuthError(err) {
				logError("[poll] auth error on @%s: %v (cookie may be expired)", entry.Handle, err)
				pollStats.TotalErrors++
			} else {
				logWarn("[poll] @%s: %v", entry.Handle, err)
			}
			continue
		}

		if len(tweets) == 0 {
			continue
		}

		lastSeenID := p.state.GetLastTweetID(strings.ToLower(entry.Handle))
		var newTweets []Tweet

		// Find tweets newer than last seen
		for _, tweet := range tweets {
			if tweet.ID == lastSeenID {
				break // found our marker
			}
			// Skip retweets if mode is not all+replies
			if tweet.IsRetweet && entry.NotifyMode != "all+replies" {
				continue
			}
			newTweets = append(newTweets, tweet)
		}

		if len(newTweets) == 0 {
			continue
		}

		// Send notifications (oldest first)
		for i := len(newTweets) - 1; i >= 0; i-- {
			tweet := newTweets[i]
			if err := p.bot.SendTweetNotification(entry.ChannelID, tweet, entry.Handle); err != nil {
				logError("[notify] @%s tweet %s: %v", entry.Handle, tweet.ID, err)
				pollStats.TotalErrors++
			} else {
				logInfo("[notify] @%s → tweet %s sent", entry.Handle, tweet.ID)
				pollStats.TotalTweets++
			}
			time.Sleep(100 * time.Millisecond) // Discord rate limit
		}

		// Update last seen
		p.state.SetLastTweetID(strings.ToLower(entry.Handle), tweets[0].ID)
		time.Sleep(300 * time.Millisecond) // X rate limit
	}

	p.state.Save()
	elapsed := time.Since(started)
	logInfo("[poll] scan done in %s", elapsed.Round(time.Second))
}

func isAuthError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "401") || strings.Contains(s, "unauthorized") || strings.Contains(s, "cookie may be expired")
}

// ReSync re-follows and re-enables notifications for all watched accounts.
// Called when cookie hash changes (account swap).
func (p *Poller) ReSync() {
	entries := p.watch.GetAll()
	xc := p.nextClient()
	logInfo("[resync] re-syncing %d accounts with new cookie", len(entries))

	for _, entry := range entries {
		// Re-follow
		if err := xc.Follow(entry.Handle); err != nil {
			logWarn("[resync] follow @%s: %v", entry.Handle, err)
		}

		// Re-enable notifications
		if err := xc.SetNotifications(entry.Handle, entry.NotifyMode); err != nil {
			logWarn("[resync] notif @%s (%s): %v", entry.Handle, entry.NotifyMode, err)
		}

		logInfo("[resync] @%s → follow + notif %s", entry.Handle, entry.NotifyMode)
		time.Sleep(500 * time.Millisecond)
	}

	// Re-seed baselines
	p.warmup()

	// Update cookie hash in state
	p.state.SetCookieHash(p.cfg.CookieHash())
	p.state.Save()

	logInfo("[resync] done")

	// Alert via webhook if configured
	if p.cfg.Twitter.AlertWebhook != "" {
		p.sendResyncAlert(len(entries))
	}
}

func (p *Poller) sendResyncAlert(count int) {
	// Simple webhook alert
	// TODO: implement via discordgo or HTTP
	logInfo("[resync] alert: re-synced %d accounts with new cookie", count)
}

// HealthCheck runs periodic cookie health checks.
func (p *Poller) HealthCheck(ctx context.Context) {
	interval := p.cfg.HealthCheckDuration()
	logInfo("[health] starting with interval %s", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, xc := range p.clients {
				if err := xc.HealthCheck(); err != nil {
					logError("[health] cookie %s failed: %v", xc.label, err)
					// TODO: send alert to Discord/Telegram
				} else {
					logDebug("[health] cookie %s OK", xc.label)
				}
			}
		}
	}
}

// ConfigWatcher watches for config file changes and triggers re-sync.
func (p *Poller) ConfigWatcher(ctx context.Context, configPath string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	var lastHash string
	data, _ := loadConfig(configPath)
	if data != nil {
		lastHash = data.CookieHash()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newCfg, err := loadConfig(configPath)
			if err != nil {
				continue
			}
			newHash := newCfg.CookieHash()
			if newHash != lastHash && newHash != "" {
				logInfo("[config] cookie change detected, triggering re-sync")
				// Reload config
				p.cfg = newCfg
				// Rebuild clients
				p.clients = make([]*XClient, len(newCfg.Twitter.Cookies))
				for i, c := range newCfg.Twitter.Cookies {
					p.clients[i] = NewXClient(c)
				}
				p.bot.xClients = p.clients
				// Re-sync
				p.ReSync()
				lastHash = newHash
			}
		}
	}
}

// RunResyncCheck checks if a re-sync is needed on startup (cookie changed while bot was down).
func (p *Poller) RunResyncCheck() {
	currentHash := p.cfg.CookieHash()
	savedHash := p.state.GetCookieHash()

	if savedHash != "" && currentHash != savedHash {
		logInfo("[startup] cookie changed (was %s, now %s), triggering re-sync", savedHash, currentHash)
		p.ReSync()
	} else {
		// Just update the hash
		p.state.SetCookieHash(currentHash)
		p.state.Save()
	}
}

// pollStats is declared here; zero-value is fine.

// Make compiler happy for unused import
var _ = fmt.Sprintf
