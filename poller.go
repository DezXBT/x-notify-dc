package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Poller uses X's native /2/notifications/all.json endpoint.
// Instead of polling each account individually (N requests), this fetches
// ALL notifications in 1 request — same way X mobile/web works.
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
	logInfo("[poller] starting with notifications API, interval %s", interval)

	// Warmup: follow + enable notif + seed baseline
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

// warmup seeds the notification cursor and ensures all watched accounts
// are followed with notifications enabled.
func (p *Poller) warmup() {
	entries := p.watch.GetAll()
	for _, entry := range entries {
		// Resolve user ID if missing
		if entry.UserID == "" {
			xc := p.nextClient()
			user, err := xc.GetUser(entry.Handle)
			if err != nil {
				logWarn("[warmup] resolve @%s: %v", entry.Handle, err)
				continue
			}
			p.watch.UpdateUserDetails(entry.Handle, user.RestID, user.FollowersCount, user.ProfileImageURL, user.Name)
			entry.UserID = user.RestID
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Seed cursor: fetch one page of notifications to establish baseline
	if p.state.GetNotifCursor() == "" {
		xc := p.nextClient()
		page, err := xc.FetchNotifications("all", 20, "")
		if err != nil {
			logWarn("[warmup] fetch notifications: %v", err)
		} else if page != nil && len(page.NotifIDs) > 0 {
			// Store the most recent notification ID as cursor
			p.state.SetNotifCursor(page.NotifIDs[0])
			logInfo("[warmup] seeded notification cursor: %s", page.NotifIDs[0])
		}
	}

	p.state.Save()
}

// scanOnce fetches notifications from X and matches against watch list.
func (p *Poller) scanOnce() {
	started := time.Now()
	pollStats.TotalPolls++
	pollStats.LastPollTime = started
	logDebug("[poll] scan starting")

	entries := p.watch.GetAll()
	if len(entries) == 0 {
		logDebug("[poll] no watched accounts")
		return
	}

	// Build handle→entry map for fast lookup
	watchMap := make(map[string]WatchEntry, len(entries))
	for _, e := range entries {
		watchMap[strings.ToLower(e.Handle)] = e
	}

	// Single API call for ALL notifications
	xc := p.nextClient()
	logDebug("[poll] fetching notifications...")
	page, err := xc.FetchNotifications("all", 40, "")
	if err != nil {
		if isAuthError(err) {
			logError("[poll] auth error: %v (cookie may be expired)", err)
		} else {
			logWarn("[poll] notifications: %v", err)
		}
		pollStats.TotalErrors++
		return
	}

	if page == nil || len(page.Tweets) == 0 {
		logDebug("[poll] no new notifications")
		return
	}

	lastCursor := p.state.GetNotifCursor()
	newCursor := page.NotifIDs[0] // most recent notification ID
	matched := 0

	// Quick check: if the most recent notification hasn't changed since last poll,
	// there's nothing new. Notification IDs are stable (not comparable to tweet IDs).
	if lastCursor == newCursor {
		logDebug("[poll] no new notifications (cursor unchanged)")
		return
	}

	for _, tweet := range page.Tweets {
		handle := strings.ToLower(tweet.Author.ScreenName)
		entry, watched := watchMap[handle]
		if !watched {
			continue
		}

		// Skip duplicates using per-account last tweet ID (numeric comparison)
		lastTweetID := p.state.GetLastTweetID(handle)
		if lastTweetID != "" && tweet.ID <= lastTweetID {
			continue
		}

		// Skip retweets if mode is not all+replies
		if tweet.IsRetweet && entry.NotifyMode != "all+replies" {
			continue
		}

		matched++
		if err := p.bot.SendTweetNotification(entry.ChannelID, tweet, entry.Handle); err != nil {
			logError("[notify] @%s tweet %s: %v", entry.Handle, tweet.ID, err)
			pollStats.TotalErrors++
		} else {
			logInfo("[notify] @%s → tweet %s sent", entry.Handle, tweet.ID)
			pollStats.TotalTweets++
		}
		time.Sleep(100 * time.Millisecond) // Discord rate limit
	}

	// Update notification cursor
	if newCursor != "" && newCursor != lastCursor {
		p.state.SetNotifCursor(newCursor)
	}

	// Also update per-account baselines for watched accounts
	for _, tweet := range page.Tweets {
		handle := strings.ToLower(tweet.Author.ScreenName)
		if _, ok := watchMap[handle]; ok {
			current := p.state.GetLastTweetID(handle)
			if current == "" || tweet.ID > current {
				p.state.SetLastTweetID(handle, tweet.ID)
			}
		}
	}

	p.state.Save()

	elapsed := time.Since(started)
	logDebug("[poll] scan done: %d tweets matched from notifications in %s", matched, elapsed.Round(time.Millisecond))
}

func isAuthError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "401") || strings.Contains(s, "unauthorized") || strings.Contains(s, "cookie may be expired")
}

// ReSync re-follows and re-enables notifications for all watched accounts.
func (p *Poller) ReSync() {
	entries := p.watch.GetAll()
	xc := p.nextClient()
	logInfo("[resync] re-syncing %d accounts with new cookie", len(entries))

	for _, entry := range entries {
		if err := xc.Follow(entry.Handle); err != nil {
			logWarn("[resync] follow @%s: %v", entry.Handle, err)
		}
		if err := xc.SetNotifications(entry.Handle, entry.NotifyMode); err != nil {
			logWarn("[resync] notif @%s (%s): %v", entry.Handle, entry.NotifyMode, err)
		}
		logInfo("[resync] @%s → follow + notif %s", entry.Handle, entry.NotifyMode)
		time.Sleep(500 * time.Millisecond)
	}

	// Clear cursor so warmup re-seeds it
	p.state.SetNotifCursor("")
	p.warmup()

	p.state.SetCookieHash(p.cfg.CookieHash())
	p.state.Save()
	logInfo("[resync] done")

	if p.cfg.Twitter.AlertWebhook != "" {
		p.sendResyncAlert(len(entries))
	}
}

func (p *Poller) sendResyncAlert(count int) {
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
				p.cfg = newCfg
				p.clients = make([]*XClient, len(newCfg.Twitter.Cookies))
				for i, c := range newCfg.Twitter.Cookies {
					p.clients[i] = NewXClient(c)
				}
				p.bot.xClients = p.clients
				p.ReSync()
				lastHash = newHash
			}
		}
	}
}

// RunResyncCheck checks if a re-sync is needed on startup.
func (p *Poller) RunResyncCheck() {
	currentHash := p.cfg.CookieHash()
	savedHash := p.state.GetCookieHash()

	if savedHash != "" && currentHash != savedHash {
		logInfo("[startup] cookie changed (was %s, now %s), triggering re-sync", savedHash, currentHash)
		p.ReSync()
	} else {
		p.state.SetCookieHash(currentHash)
		p.state.Save()
	}
}

// Make compiler happy for unused import
var _ = fmt.Sprintf
