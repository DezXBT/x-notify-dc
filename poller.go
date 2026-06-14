package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Poller uses X's native /2/notifications/all.json endpoint.
// Supports two modes:
//   - Real-time: WebSocket live_pipeline pushes → instant REST poll on event
//   - Fallback: adaptive polling (5s active, 30s idle)
type Poller struct {
	cfg       *Config
	watch     *WatchManager
	state     *SeenState
	clients   []*XClient
	bot       *DiscordBot
	clientIdx int
	ws        *wsNotifier
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
// Tries WebSocket first for real-time; falls back to adaptive polling.
func (p *Poller) Run(ctx context.Context) {
	// Warmup: follow + enable notif + seed baseline
	p.warmup()

	// Try WebSocket real-time mode
	p.ws = newWSNotifier(p.cfg.Twitter.Cookies)
	if p.ws.IsAvailable() {
		if err := p.ws.Start(ctx, func() {
			// WS push received → immediate REST poll
			p.scanOnce()
		}); err != nil {
			logWarn("[poller] WebSocket unavailable, falling back to polling: %v", err)
			p.runPolling(ctx)
		} else {
			// WS connected — still run periodic polling at idle interval as safety net
			logInfo("[poller] real-time mode active, safety-net poll every %s", p.cfg.IdlePollIntervalDuration())
			p.runSafetyNet(ctx)
		}
	} else {
		p.runPolling(ctx)
	}
}

// runPolling runs adaptive polling: fast interval when active, slow when idle.
func (p *Poller) runPolling(ctx context.Context) {
	baseInterval := p.cfg.PollIntervalDuration()
	idleInterval := p.cfg.IdlePollIntervalDuration()
	idleThreshold := p.cfg.IdleThresholdDuration()

	interval := baseInterval
	lastActivity := time.Now()

	logInfo("[poller] starting adaptive polling: active=%s idle=%s threshold=%s", baseInterval, idleInterval, idleThreshold)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logInfo("[poller] stopping")
			if p.ws != nil {
				p.ws.Stop()
			}
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logError("[poller] recovered from panic: %v", r)
						pollStats.TotalErrors++
					}
				}()
				hadActivity := p.scanOnceWithResult()
				if hadActivity {
					lastActivity = time.Now()
					if interval != baseInterval {
						interval = baseInterval
						ticker.Reset(interval)
						logDebug("[poller] activity detected, switching to fast polling: %s", interval)
					}
				} else if time.Since(lastActivity) > idleThreshold && interval != idleInterval {
					interval = idleInterval
					ticker.Reset(interval)
					logDebug("[poller] idle for %s, switching to slow polling: %s", idleThreshold, interval)
				}
			}()
		}
	}
}

// runSafetyNet runs periodic polling at idle interval as backup to WS.
func (p *Poller) runSafetyNet(ctx context.Context) {
	interval := p.cfg.IdlePollIntervalDuration()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logInfo("[poller] stopping")
			if p.ws != nil {
				p.ws.Stop()
			}
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logError("[poller] recovered from panic: %v", r)
					}
				}()
				p.scanOnce()
			}()
		}
	}
}

// warmup resolves user IDs for watched accounts.
func (p *Poller) warmup() {
	entries := p.watch.GetAll()
	for _, entry := range entries {
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

	// Ensure ALL clients follow+bell to ALL watched accounts (resilience)
	p.ensureAllFollowed(entries)

	// Seed baseline: get latest tweet ID per watched account to avoid alerting old tweets
	for _, entry := range entries {
		if entry.UserID == "" {
			continue
		}
		xc := p.clients[0] // use first client for baseline seed
		tweets, err := xc.GetUserTweets(entry.UserID, 1)
		if err != nil {
			logWarn("[warmup] fetch @%s timeline: %v", entry.Handle, err)
			continue
		}
		if len(tweets) > 0 {
			current := p.state.GetLastTweetID(strings.ToLower(entry.Handle))
			if current == "" || tweets[0].ID > current {
				p.state.SetLastTweetID(strings.ToLower(entry.Handle), tweets[0].ID)
				logInfo("[warmup] seeded @%s baseline: tweet %s", entry.Handle, tweets[0].ID)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	p.state.Save()
}

// ensureAllFollowed makes every cookie account follow+bell every watched target.
// This ensures device_follow.json always returns data regardless of which client is used.
func (p *Poller) ensureAllFollowed(entries []WatchEntry) {
	for _, entry := range entries {
		if entry.UserID == "" {
			continue
		}
		for _, xc := range p.clients {
			// Follow (ignore "already following" errors)
			if err := xc.Follow(entry.Handle); err != nil {
				if !strings.Contains(err.Error(), "403") {
					logWarn("[warmup] %s follow @%s: %v", xc.label, entry.Handle, err)
				}
			}
			// Enable bell notifications
			if err := xc.SetNotifications(entry.Handle, entry.NotifyMode); err != nil {
				logWarn("[warmup] %s notif @%s: %v", xc.label, entry.Handle, err)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
}

// scanOnce fetches notifications from X and matches against watch list.
func (p *Poller) scanOnce() {
	p.scanOnceWithResult()
}

// scanOnceWithResult returns true if new tweets were found and processed.
// Uses /2/notifications/device_follow.json for efficient single-call detection
// of new tweets from all bell-enabled accounts, with per-account UserTweets fallback.
func (p *Poller) scanOnceWithResult() bool {
	started := time.Now()
	pollStats.TotalPolls++
	pollStats.LastPollTime = started
	logDebug("[poll] scan starting (device_follow mode)")

	entries := p.watch.GetAll()
	if len(entries) == 0 {
		logDebug("[poll] no watched accounts")
		return false
	}

	// Build lookup: userID -> []WatchEntry (one user can be in multiple channels)
	userEntries := make(map[string][]WatchEntry)
	for _, entry := range entries {
		if entry.UserID != "" {
			userEntries[entry.UserID] = append(userEntries[entry.UserID], entry)
		}
	}

	matched := 0

	// Primary: single API call via device_follow
	xc := p.nextClient()
	dfTweets, err := xc.GetDeviceFollowTweets(40)
	if err != nil {
		if isAuthError(err) {
			logError("[poll] device_follow auth error: %v (cookie may be expired)", err)
		} else {
			logWarn("[poll] device_follow failed: %v — falling back to per-account polling", err)
		}
		pollStats.TotalErrors++
		// Fallback to per-account polling
		return p.scanOncePerAccount()
	}

	logDebug("[poll] device_follow returned %d tweets", len(dfTweets))

	// Match tweets to watched accounts
	for i := range dfTweets {
		dft := &dfTweets[i]
		watchEntries, isWatched := userEntries[dft.UserID]
		if !isWatched {
			continue
		}

		for _, entry := range watchEntries {
			lastTweetID := p.state.GetLastTweetID(strings.ToLower(entry.Handle))

			// Skip old tweets
			if lastTweetID != "" && dft.ID <= lastTweetID {
				continue
			}

			// Skip retweets if mode is not all+replies
			if dft.IsRetweet && entry.NotifyMode != "all+replies" {
				continue
			}

			matched++
			tweet := dft.ToTweet()
			if err := p.bot.SendTweetNotification(entry.ChannelID, tweet, entry.Handle); err != nil {
				logError("[notify] @%s tweet %s: %v", entry.Handle, dft.ID, err)
				pollStats.TotalErrors++
			} else {
				logInfo("[notify] @%s → tweet %s sent (via device_follow)", entry.Handle, dft.ID)
				pollStats.TotalTweets++
			}
			time.Sleep(100 * time.Millisecond) // Discord rate limit
		}
	}

	// Update baselines: for each watched user, find newest tweet in response
	for userID, watchEnts := range userEntries {
		var newestID string
		for i := range dfTweets {
			if dfTweets[i].UserID == userID {
				if newestID == "" || dfTweets[i].ID > newestID {
					newestID = dfTweets[i].ID
				}
			}
		}
		if newestID != "" {
			for _, entry := range watchEnts {
				current := p.state.GetLastTweetID(strings.ToLower(entry.Handle))
				if current == "" || newestID > current {
					p.state.SetLastTweetID(strings.ToLower(entry.Handle), newestID)
				}
			}
		}
	}

	if matched > 0 {
		p.state.Save()
	}

	elapsed := time.Since(started)
	logDebug("[poll] scan done: %d new tweets found in %s (device_follow)", matched, elapsed.Round(time.Millisecond))
	return matched > 0
}

// scanOncePerAccount is the legacy fallback that polls each watched account individually.
func (p *Poller) scanOncePerAccount() bool {
	entries := p.watch.GetAll()
	matched := 0

	for _, entry := range entries {
		xc := p.nextClient()
		tweets, err := xc.GetUserTweets(entry.UserID, 5)
		if err != nil {
			if isAuthError(err) {
				logError("[poll] auth error for @%s: %v (cookie may be expired)", entry.Handle, err)
			} else {
				logWarn("[poll] @%s timeline: %v", entry.Handle, err)
			}
			pollStats.TotalErrors++
			continue
		}

		if len(tweets) == 0 {
			continue
		}

		lastTweetID := p.state.GetLastTweetID(strings.ToLower(entry.Handle))

		for _, tweet := range tweets {
			if lastTweetID != "" && tweet.ID <= lastTweetID {
				break
			}
			if tweet.IsRetweet && entry.NotifyMode != "all+replies" {
				continue
			}

			matched++
			if err := p.bot.SendTweetNotification(entry.ChannelID, tweet, entry.Handle); err != nil {
				logError("[notify] @%s tweet %s: %v", entry.Handle, tweet.ID, err)
				pollStats.TotalErrors++
			} else {
				logInfo("[notify] @%s → tweet %s sent (fallback)", entry.Handle, tweet.ID)
				pollStats.TotalTweets++
			}
			time.Sleep(100 * time.Millisecond)
		}

		if len(tweets) > 0 {
			newestID := tweets[0].ID
			current := p.state.GetLastTweetID(strings.ToLower(entry.Handle))
			if current == "" || newestID > current {
				p.state.SetLastTweetID(strings.ToLower(entry.Handle), newestID)
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	if matched > 0 {
		p.state.Save()
	}
	return matched > 0
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
