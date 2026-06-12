package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// DiscordBot manages Discord interactions and embeds.
type DiscordBot struct {
	session  *discordgo.Session
	cfg      *Config
	watch    *WatchManager
	xClients []*XClient
	clientIdx int
}

func NewDiscordBot(cfg *Config, watch *WatchManager, xClients []*XClient) (*DiscordBot, error) {
	dg, err := discordgo.New("Bot " + cfg.Discord.BotToken)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	dg.Identify.Intents = discordgo.IntentGuilds

	bot := &DiscordBot{
		session:  dg,
		cfg:      cfg,
		watch:    watch,
		xClients: xClients,
	}
	return bot, nil
}

func (db *DiscordBot) nextClient() *XClient {
	c := db.xClients[db.clientIdx]
	db.clientIdx = (db.clientIdx + 1) % len(db.xClients)
	return c
}

func (db *DiscordBot) Open() error {
	return db.session.Open()
}

func (db *DiscordBot) Close() {
	db.session.Close()
}

// RegisterCommands registers slash commands.
func (db *DiscordBot) RegisterCommands(guildID string) error {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "setup",
			Description: "Set the default notification channel",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "Channel for notifications",
					Required:    true,
				},
			},
		},
		{
			Name:        "add",
			Description: "Add an X/Twitter account to watch for new tweets",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "handle",
					Description: "X/Twitter handle or URL (e.g. elonmusk or https://x.com/elonmusk)",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "Channel to send notifications (default: this channel)",
					Required:    false,
				},
			},
		},
		{
			Name:        "remove",
			Description: "Stop watching an X/Twitter account",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "handle",
					Description: "X/Twitter handle to remove",
					Required:    true,
				},
			},
		},
		{
			Name:        "list",
			Description: "Show all watched X/Twitter accounts",
		},
		{
			Name:        "settings",
			Description: "Change notification mode for a watched account",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "handle",
					Description: "X/Twitter handle",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "mode",
					Description: "Notification mode",
					Required:    true,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "All Posts", Value: "all"},
						{Name: "All Posts + Replies", Value: "all+replies"},
						{Name: "Off", Value: "off"},
					},
				},
			},
		},
		{
			Name:        "status",
			Description: "Show bot health and statistics",
		},
	}

	for _, cmd := range commands {
		_, err := db.session.ApplicationCommandCreate(db.session.State.User.ID, guildID, cmd)
		if err != nil {
			return fmt.Errorf("register command %s: %w", cmd.Name, err)
		}
		logInfo("[discord] registered command: /%s", cmd.Name)
	}
	return nil
}

// RegisterHandlers registers interaction handlers.
func (db *DiscordBot) RegisterHandlers() {
	db.session.AddHandler(db.handleInteraction)
}

// ────────────────────────────────────────────────────────
// Interaction Handler
// ────────────────────────────────────────────────────────

var startTime = time.Now()

func (db *DiscordBot) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	switch data.Name {
	case "setup":
		db.handleSetup(s, i, data)
	case "add":
		db.handleAdd(s, i, data)
	case "remove":
		db.handleRemove(s, i, data)
	case "list":
		db.handleList(s, i)
	case "settings":
		db.handleSettings(s, i, data)
	case "status":
		db.handleStatus(s, i)
	}
}

// ────────────────────────────────────────────────────────
// /setup
// ────────────────────────────────────────────────────────

func (db *DiscordBot) handleSetup(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	ch := getChannelOption(data.Options, "channel")
	if ch == "" {
		respond(s, i, "❌ Please specify a channel.")
		return
	}

	// Update config in memory
	db.cfg.Discord.DefaultChannel = ch

	// Update config.yaml file
	if err := updateConfigDefaultChannel(ch); err != nil {
		logWarn("[setup] failed to update config.yaml: %v", err)
	}

	embed := &discordgo.MessageEmbed{
		Title:       "✅ Notification Channel Set",
		Description: fmt.Sprintf("All new notifications will be sent to <#%s>", ch),
		Color:       0x00FF00,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Channel", Value: fmt.Sprintf("<#%s>", ch), Inline: true},
			{Name: "Channel ID", Value: ch, Inline: true},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("x-notify-dc | %s WIB", time.Now().In(db.cfg.Timezone()).Format("02/01/2006, 15:04:05")),
		},
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
	logInfo("[setup] default channel set to %s by %s", ch, i.Member.User.Username)
}

// updateConfigDefaultChannel updates the default_channel in config.yaml
func updateConfigDefaultChannel(channelID string) error {
	path := "config.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)
	// Simple string replacement
	if strings.Contains(content, "default_channel:") {
		// Replace existing
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if strings.Contains(line, "default_channel:") {
				lines[i] = fmt.Sprintf("  default_channel: \"%s\"", channelID)
			}
		}
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
	}
	return nil
}

// ────────────────────────────────────────────────────────
// /add
// ────────────────────────────────────────────────────────

func (db *DiscordBot) handleAdd(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	handle := getStringOption(data.Options, "handle")
	handle = normalizeHandle(handle)
	if handle == "" {
		respond(s, i, "❌ Invalid handle. Use a username like `elonmusk` or a URL like `https://x.com/elonmusk`")
		return
	}

	// Determine target channel: /add channel param > default_channel from config
	channelID := db.cfg.Discord.DefaultChannel
	if ch := getChannelOption(data.Options, "channel"); ch != "" {
		channelID = ch
	}

	// Check if already watching
	if _, exists := db.watch.Get(handle); exists {
		respond(s, i, fmt.Sprintf("⚠️ **@%s** is already being watched.", handle))
		return
	}

	// Defer response since we'll make API calls
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Get user info from X
	xc := db.nextClient()
	user, err := xc.GetUser(handle)
	if err != nil {
		editResponse(s, i, fmt.Sprintf("❌ Failed to find **@%s** on X: %v", handle, err))
		return
	}

	// Follow
	if err := xc.Follow(handle); err != nil {
		logWarn("[add] follow @%s failed: %v", handle, err)
		// Non-fatal: maybe already following
	}

	// Enable notifications (default: all posts)
	if err := xc.SetNotifications(handle, "all"); err != nil {
		logWarn("[add] enable notif @%s failed: %v", handle, err)
	}

	// Seed baseline (get last tweet ID to avoid alerting old tweets)
	var lastTweetID string
	if tweets, err := xc.GetUserTweets(user.RestID, 1); err == nil && len(tweets) > 0 {
		lastTweetID = tweets[0].ID
	}

	// Save to watch list
	entry := WatchEntry{
		Handle:          user.ScreenName,
		UserID:          user.RestID,
		AddedBy:         i.Member.User.ID,
		ChannelID:       channelID,
		GuildID:         i.GuildID,
		NotifyMode:      "all",
		AddedAt:         time.Now(),
		FollowersCount:  user.FollowersCount,
		ProfileImageURL: user.ProfileImageURL,
		Name:            user.Name,
	}
	if !db.watch.Add(entry) {
		editResponse(s, i, fmt.Sprintf("⚠️ **@%s** is already being watched.", handle))
		return
	}

	// Save baseline
	if lastTweetID != "" {
		db.watch.updateState(handle, lastTweetID)
	}

	// Build success embed
	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("✅ Now watching @%s", user.ScreenName),
		URL:   fmt.Sprintf("https://x.com/%s", user.ScreenName),
		Color: 0x00FF00,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Name", Value: user.Name, Inline: true},
			{Name: "Followers", Value: FormatNumber(user.FollowersCount), Inline: true},
			{Name: "Notifications", Value: "All Posts", Inline: true},
			{Name: "Channel", Value: fmt.Sprintf("<#%s>", channelID), Inline: true},
		},
	}
	if user.ProfileImageURL != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: user.ProfileImageURL}
	}
	if user.Description != "" {
		desc := user.Description
		if len(desc) > 200 {
			desc = desc[:197] + "..."
		}
		embed.Description = desc
	}
	embed.Footer = &discordgo.MessageEmbedFooter{
		Text: fmt.Sprintf("x-notify-dc | %s WIB", time.Now().In(db.cfg.Timezone()).Format("02/01/2006, 15:04:05")),
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})

	logInfo("[add] @%s added by %s → channel %s", user.ScreenName, i.Member.User.Username, channelID)
}

// ────────────────────────────────────────────────────────
// /remove
// ────────────────────────────────────────────────────────

func (db *DiscordBot) handleRemove(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	handle := getStringOption(data.Options, "handle")
	handle = normalizeHandle(handle)
	if handle == "" {
		respond(s, i, "❌ Invalid handle.")
		return
	}

	entry, exists := db.watch.Get(handle)
	if !exists {
		respond(s, i, fmt.Sprintf("❌ **@%s** is not being watched.", handle))
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Unfollow + disable notifications
	xc := db.nextClient()
	if err := xc.Unfollow(entry.Handle); err != nil {
		logWarn("[remove] unfollow @%s: %v", entry.Handle, err)
	}
	if err := xc.SetNotifications(entry.Handle, "off"); err != nil {
		logWarn("[remove] disable notif @%s: %v", entry.Handle, err)
	}

	db.watch.Remove(handle)

	editResponse(s, i, fmt.Sprintf("✅ Stopped watching **@%s**. Unfollowed and notifications disabled.", entry.Handle))
	logInfo("[remove] @%s removed", entry.Handle)
}

// ────────────────────────────────────────────────────────
// /list
// ────────────────────────────────────────────────────────

func (db *DiscordBot) handleList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	entries := db.watch.GetAll()
	if len(entries) == 0 {
		respond(s, i, "📭 No accounts being watched. Use `/add <handle>` to start.")
		return
	}

	loc := db.cfg.Timezone()
	var fields []*discordgo.MessageEmbedField
	for idx, e := range entries {
		modeLabel := "All Posts"
		switch e.NotifyMode {
		case "all+replies":
			modeLabel = "All + Replies"
		case "off":
			modeLabel = "Off"
		}
		value := fmt.Sprintf("👥 %s · 🔔 %s · Added %s",
			FormatCompact(e.FollowersCount), modeLabel, TrimSince(e.AddedAt, loc))
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%d. @%s", idx+1, e.Handle),
			Value:  value,
			Inline: false,
		})
	}

	embed := &discordgo.MessageEmbed{
		Title:  fmt.Sprintf("📋 Watched Accounts (%d)", len(entries)),
		Color:  0x1DA1F2,
		Fields: fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("x-notify-dc | %s WIB", time.Now().In(loc).Format("02/01/2006, 15:04:05")),
		},
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

// ────────────────────────────────────────────────────────
// /settings
// ────────────────────────────────────────────────────────

func (db *DiscordBot) handleSettings(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	handle := getStringOption(data.Options, "handle")
	mode := getStringOption(data.Options, "mode")
	handle = normalizeHandle(handle)

	if handle == "" || mode == "" {
		respond(s, i, "❌ Both handle and mode are required.")
		return
	}

	entry, exists := db.watch.Get(handle)
	if !exists {
		respond(s, i, fmt.Sprintf("❌ **@%s** is not being watched.", handle))
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Update notification on X
	xc := db.nextClient()
	if err := xc.SetNotifications(entry.Handle, mode); err != nil {
		editResponse(s, i, fmt.Sprintf("❌ Failed to update notifications for **@%s**: %v", entry.Handle, err))
		return
	}

	db.watch.UpdateNotifyMode(handle, mode)

	modeLabel := map[string]string{
		"all":         "All Posts",
		"all+replies": "All Posts + Replies",
		"off":         "Off",
	}[mode]

	editResponse(s, i, fmt.Sprintf("✅ **@%s** notifications set to **%s**", entry.Handle, modeLabel))
	logInfo("[settings] @%s → %s", entry.Handle, mode)
}

// ────────────────────────────────────────────────────────
// /status
// ────────────────────────────────────────────────────────

var pollStats struct {
	LastPollTime  time.Time
	TotalPolls    int64
	TotalTweets   int64
	TotalErrors   int64
}

func (db *DiscordBot) handleStatus(s *discordgo.Session, i *discordgo.InteractionCreate) {
	uptime := time.Since(startTime)
	loc := db.cfg.Timezone()

	var lastPoll string
	if pollStats.LastPollTime.IsZero() {
		lastPoll = "Never"
	} else {
		lastPoll = TrimSince(pollStats.LastPollTime, loc)
	}

	// Cookie health
	xc := db.xClients[0]
	cookieStatus := "✅ Alive"
	if err := xc.HealthCheck(); err != nil {
		cookieStatus = fmt.Sprintf("❌ %v", err)
	}

	embed := &discordgo.MessageEmbed{
		Title: "🤖 Bot Status",
		Color: 0x1DA1F2,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Uptime", Value: fmt.Sprintf("%s", uptime.Round(time.Second)), Inline: true},
			{Name: "Watched", Value: fmt.Sprintf("%d accounts", db.watch.Count()), Inline: true},
			{Name: "Cookies", Value: fmt.Sprintf("%d active", len(db.xClients)), Inline: true},
			{Name: "Last Poll", Value: lastPoll, Inline: true},
			{Name: "Total Polls", Value: fmt.Sprintf("%d", pollStats.TotalPolls), Inline: true},
			{Name: "Tweets Detected", Value: fmt.Sprintf("%d", pollStats.TotalTweets), Inline: true},
			{Name: "Errors", Value: fmt.Sprintf("%d", pollStats.TotalErrors), Inline: true},
			{Name: "Cookie Health", Value: cookieStatus, Inline: true},
			{Name: "Poll Interval", Value: db.cfg.Tracking.PollInterval, Inline: true},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("x-notify-dc | %s WIB", time.Now().In(loc).Format("02/01/2006, 15:04:05")),
		},
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

// ────────────────────────────────────────────────────────
// Tweet Notification Embed
// ────────────────────────────────────────────────────────

// SendTweetNotification sends a tweet embed to a Discord channel.
func (db *DiscordBot) SendTweetNotification(channelID string, tweet Tweet) error {
	loc := db.cfg.Timezone()

	// Build description
	description := tweet.Text
	// Clean up t.co URLs in text — replace with expanded versions
	// (X shortens URLs in full_text)

	// Build embed
	embed := &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			Name:    fmt.Sprintf("%s (@%s)", tweet.Author.Name, tweet.Author.ScreenName),
			URL:     fmt.Sprintf("https://x.com/%s", tweet.Author.ScreenName),
			IconURL: tweet.Author.AvatarURL,
		},
		Description: description,
		URL:         tweet.TweetURL,
		Color:       0x1DA1F2,
	}

	// Metrics field
	var metricsParts []string
	if tweet.Metrics.Likes > 0 {
		metricsParts = append(metricsParts, fmt.Sprintf("❤️ %s", FormatCompact(tweet.Metrics.Likes)))
	}
	if tweet.Metrics.Retweets > 0 {
		metricsParts = append(metricsParts, fmt.Sprintf("🔁 %s", FormatCompact(tweet.Metrics.Retweets)))
	}
	if tweet.Metrics.Replies > 0 {
		metricsParts = append(metricsParts, fmt.Sprintf("💬 %s", FormatCompact(tweet.Metrics.Replies)))
	}
	if tweet.Metrics.Views > 0 {
		metricsParts = append(metricsParts, fmt.Sprintf("👁️ %s", FormatCompact(tweet.Metrics.Views)))
	}
	if len(metricsParts) > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Engagement",
			Value:  joinStrings(metricsParts, "  "),
			Inline: false,
		})
	}

	// Media: use first image as embed image
	if len(tweet.MediaURLs) > 0 {
		embed.Image = &discordgo.MessageEmbedImage{URL: tweet.MediaURLs[0]}
	}

	// Author avatar as thumbnail if no media
	if len(tweet.MediaURLs) == 0 && tweet.Author.AvatarURL != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: tweet.Author.AvatarURL}
	}

	embed.Footer = &discordgo.MessageEmbedFooter{
		Text: fmt.Sprintf("x-notify-dc | %s WIB", time.Now().In(loc).Format("02/01/2006, 15:04:05")),
	}

	_, err := db.session.ChannelMessageSendEmbed(channelID, embed)
	return err
}

// ────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
		},
	})
}

func editResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
	})
}

func getStringOption(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}

func getChannelOption(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionChannel {
			return opt.ChannelValue(nil).ID
		}
	}
	return ""
}

func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}

// updateState is a helper on WatchManager to update seen tweet state.
// The actual SeenState is accessed via a global or injected reference.
// This is a placeholder — the real update happens in poller.go.
func (wm *WatchManager) updateState(handle, lastTweetID string) {
	// This is called from discord.go for initial seed.
	// The poller manages ongoing state updates.
}
