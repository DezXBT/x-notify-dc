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
	session   *discordgo.Session
	cfg       *Config
	watch     *WatchManager
	xClients  []*XClient
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
			Name:        "cookie",
			Description: "Manage X/Twitter cookies",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "add",
					Description: "Add a new X cookie pair",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "auth_token",
							Description: "auth_token cookie value",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "ct0",
							Description: "ct0 cookie value",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "label",
							Description: "Label for this cookie (e.g. main, backup)",
							Required:    false,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "remove",
					Description: "Remove a cookie by label",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "label",
							Description: "Label of the cookie to remove",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list",
					Description: "Show all configured cookies (masked)",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "health",
					Description: "Check health of all cookies",
				},
			},
		},
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
	case "cookie":
		db.handleCookie(s, i, data)
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
// /cookie
// ────────────────────────────────────────────────────────

func (db *DiscordBot) handleCookie(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if len(data.Options) == 0 {
		respond(s, i, "❌ Use `/cookie add`, `/cookie remove`, `/cookie list`, or `/cookie health`")
		return
	}

	sub := data.Options[0]
	switch sub.Name {
	case "add":
		db.handleCookieAdd(s, i, sub)
	case "remove":
		db.handleCookieRemove(s, i, sub)
	case "list":
		db.handleCookieList(s, i)
	case "health":
		db.handleCookieHealth(s, i)
	}
}

func (db *DiscordBot) handleCookieAdd(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	authToken := getSubStringOption(sub.Options, "auth_token")
	ct0 := getSubStringOption(sub.Options, "ct0")
	label := getSubStringOption(sub.Options, "label")

	if authToken == "" || ct0 == "" {
		respond(s, i, "❌ auth_token and ct0 are required.")
		return
	}
	if label == "" {
		label = fmt.Sprintf("cookie-%d", len(db.cfg.Twitter.Cookies)+1)
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Test the cookie
	testClient := NewXClient(CookiePair{AuthToken: authToken, Ct0: ct0, Label: label})
	if err := testClient.HealthCheck(); err != nil {
		editResponse(s, i, fmt.Sprintf("❌ Cookie test failed: %v\nCookie NOT added.", err))
		return
	}

	// Check for duplicate
	for _, c := range db.cfg.Twitter.Cookies {
		if c.AuthToken == authToken {
			editResponse(s, i, "⚠️ This auth_token already exists in config.")
			return
		}
	}

	// Add to config
	newCookie := CookiePair{AuthToken: authToken, Ct0: ct0, Label: label}
	db.cfg.Twitter.Cookies = append(db.cfg.Twitter.Cookies, newCookie)
	db.xClients = append(db.xClients, testClient)

	// Update config.yaml
	if err := addCookieToConfig(newCookie); err != nil {
		logWarn("[cookie] failed to update config.yaml: %v", err)
	}

	embed := &discordgo.MessageEmbed{
		Title: "✅ Cookie Added",
		Color: 0x00FF00,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Label", Value: label, Inline: true},
			{Name: "Auth Token", Value: maskString(authToken), Inline: true},
			{Name: "CT0", Value: maskString(ct0), Inline: true},
			{Name: "Health", Value: "✅ OK", Inline: true},
			{Name: "Total Cookies", Value: fmt.Sprintf("%d", len(db.cfg.Twitter.Cookies)), Inline: true},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("x-notify-dc | %s WIB", time.Now().In(db.cfg.Timezone()).Format("02/01/2006, 15:04:05")),
		},
	}

	editResponseEmbed(s, i, embed)
	logInfo("[cookie] added '%s' by %s", label, i.Member.User.Username)
}

func (db *DiscordBot) handleCookieRemove(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	label := getSubStringOption(sub.Options, "label")
	if label == "" {
		respond(s, i, "❌ Label is required.")
		return
	}

	// Find and remove
	found := false
	var newCookies []CookiePair
	var newClients []*XClient
	for idx, c := range db.cfg.Twitter.Cookies {
		if c.Label == label {
			found = true
			continue
		}
		newCookies = append(newCookies, c)
		if idx < len(db.xClients) {
			newClients = append(newClients, db.xClients[idx])
		}
	}

	if !found {
		respond(s, i, fmt.Sprintf("❌ Cookie with label '%s' not found.", label))
		return
	}

	// Can't remove last cookie
	if len(newCookies) == 0 {
		respond(s, i, "❌ Cannot remove the last cookie. Bot needs at least one.")
		return
	}

	db.cfg.Twitter.Cookies = newCookies
	db.xClients = newClients

	// Update config.yaml
	if err := removeCookieFromConfig(label); err != nil {
		logWarn("[cookie] failed to update config.yaml: %v", err)
	}

	embed := &discordgo.MessageEmbed{
		Title:       "✅ Cookie Removed",
		Description: fmt.Sprintf("Cookie '%s' has been removed.", label),
		Color:       0xFF6B6B,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Remaining", Value: fmt.Sprintf("%d cookie(s)", len(db.cfg.Twitter.Cookies)), Inline: true},
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
	logInfo("[cookie] removed '%s' by %s", label, i.Member.User.Username)
}

func (db *DiscordBot) handleCookieList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(db.cfg.Twitter.Cookies) == 0 {
		respond(s, i, "📭 No cookies configured.")
		return
	}

	var fields []*discordgo.MessageEmbedField
	for idx, c := range db.cfg.Twitter.Cookies {
		label := c.Label
		if label == "" {
			label = fmt.Sprintf("cookie-%d", idx+1)
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%d. %s", idx+1, label),
			Value:  fmt.Sprintf("Auth: `%s`\nCT0: `%s`", maskString(c.AuthToken), maskString(c.Ct0)),
			Inline: false,
		})
	}

	embed := &discordgo.MessageEmbed{
		Title:  fmt.Sprintf("🍪 Configured Cookies (%d)", len(db.cfg.Twitter.Cookies)),
		Color:  0x1DA1F2,
		Fields: fields,
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
}

func (db *DiscordBot) handleCookieHealth(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	var fields []*discordgo.MessageEmbedField
	alive := 0

	for idx, xc := range db.xClients {
		label := xc.label
		if label == "" {
			label = fmt.Sprintf("cookie-%d", idx+1)
		}

		status := "✅ OK"
		if err := xc.HealthCheck(); err != nil {
			status = fmt.Sprintf("❌ %v", err)
		} else {
			alive++
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   label,
			Value:  status,
			Inline: true,
		})
	}

	color := 0x00FF00
	if alive == 0 {
		color = 0xFF0000
	} else if alive < len(db.xClients) {
		color = 0xFFAA00
	}

	embed := &discordgo.MessageEmbed{
		Title:  fmt.Sprintf("🏥 Cookie Health (%d/%d alive)", alive, len(db.xClients)),
		Color:  color,
		Fields: fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("x-notify-dc | %s WIB", time.Now().In(db.cfg.Timezone()).Format("02/01/2006, 15:04:05")),
		},
	}

	editResponseEmbed(s, i, embed)
}

// ────────────────────────────────────────────────────────
// Cookie Config Helpers
// ────────────────────────────────────────────────────────

func addCookieToConfig(cookie CookiePair) error {
	return updateConfigYAML(func(content string) string {
		// Find the last cookie entry and append after it
		// Simple approach: find "cookies:" section and add entry
		lines := strings.Split(content, "\n")
		var result []string
		inCookies := false
		lastCookieIdx := -1

		for idx, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "cookies:") {
				inCookies = true
			}
			if inCookies && strings.HasPrefix(trimmed, "- auth_token:") {
				lastCookieIdx = idx
			}
			result = append(result, line)
		}

		if lastCookieIdx >= 0 {
			// Insert new cookie after last one
			newEntry := []string{
				fmt.Sprintf("    - auth_token: \"%s\"", cookie.AuthToken),
				fmt.Sprintf("      ct0: \"%s\"", cookie.Ct0),
			}
			if cookie.Label != "" {
				newEntry = append(newEntry, fmt.Sprintf("      label: \"%s\"", cookie.Label))
			}
			// Insert after lastCookieIdx
			var final []string
			final = append(final, result[:lastCookieIdx+1]...)
			final = append(final, newEntry...)
			final = append(final, result[lastCookieIdx+1:]...)
			return strings.Join(final, "\n")
		}
		return content
	})
}

func removeCookieFromConfig(label string) error {
	return updateConfigYAML(func(content string) string {
		lines := strings.Split(content, "\n")
		var result []string
		skipUntilNext := false

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)

			// Detect start of cookie block with matching label
			if strings.HasPrefix(trimmed, "- auth_token:") && !skipUntilNext {
				// Check if next non-empty line has our label
				// For now, just mark for potential skip
				skipUntilNext = false // reset
			}

			// Check for label line
			if strings.HasPrefix(trimmed, "label:") && strings.Contains(trimmed, label) {
				// Remove this entire block - go back and remove the auth_token line too
				if len(result) > 0 {
					result = result[:len(result)-1] // remove the "- auth_token:" line
				}
				continue // skip this label line
			}

			// Check if this is a ct0 line right after we might have removed something
			if strings.HasPrefix(trimmed, "ct0:") && len(result) > 0 {
				prevTrimmed := strings.TrimSpace(result[len(result)-1])
				// If previous line was removed (or is another cookie), this ct0 is orphaned
				if !strings.HasPrefix(prevTrimmed, "auth_token:") {
					continue // skip orphaned ct0
				}
			}

			result = append(result, line)
		}
		return strings.Join(result, "\n")
	})
}

func updateConfigYAML(transform func(string) string) error {
	path := "config.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	newContent := transform(string(data))
	return os.WriteFile(path, []byte(newContent), 0644)
}

func maskString(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func getSubStringOption(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}

func editResponseEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	embeds := []*discordgo.MessageEmbed{embed}
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &embeds,
	})
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

	// Follow+bell with ALL remaining clients (resilience: if primary cookie dies, others have data)
	for _, other := range db.xClients {
		if other == xc {
			continue // already did this one above
		}
		if err := other.Follow(handle); err != nil {
			if !strings.Contains(err.Error(), "403") {
				logWarn("[add] %s follow @%s: %v", other.label, handle, err)
			}
		}
		if err := other.SetNotifications(handle, "all"); err != nil {
			logWarn("[add] %s notif @%s: %v", other.label, handle, err)
		}
		time.Sleep(300 * time.Millisecond)
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

	// Unfollow + disable notifications from ALL clients
	for _, xc := range db.xClients {
		if err := xc.Unfollow(entry.Handle); err != nil {
			if !strings.Contains(err.Error(), "404") {
				logWarn("[remove] %s unfollow @%s: %v", xc.label, entry.Handle, err)
			}
		}
		if err := xc.SetNotifications(entry.Handle, "off"); err != nil {
			logWarn("[remove] %s disable notif @%s: %v", xc.label, entry.Handle, err)
		}
		time.Sleep(300 * time.Millisecond)
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

	// Update notification on X — ALL accounts
	for _, xc := range db.xClients {
		if err := xc.SetNotifications(entry.Handle, mode); err != nil {
			logWarn("[settings] %s notif @%s (%s): %v", xc.label, entry.Handle, mode, err)
		}
		time.Sleep(300 * time.Millisecond)
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
	LastPollTime time.Time
	TotalPolls   int64
	TotalTweets  int64
	TotalErrors  int64
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
func (db *DiscordBot) SendTweetNotification(channelID string, tweet Tweet, watcherHandle string) error {
	loc := db.cfg.Timezone()

	// Build embed
	embed := &discordgo.MessageEmbed{
		URL:   tweet.TweetURL,
		Color: 0xFFD700,
	}

	if tweet.IsRetweet {
		// Retweet: show who retweeted and the original author
		embed.Author = &discordgo.MessageEmbedAuthor{
			Name:    fmt.Sprintf("🔁 @%s retweeted @%s", watcherHandle, tweet.Author.ScreenName),
			URL:     tweet.TweetURL,
			IconURL: tweet.Author.AvatarURL,
		}
	} else {
		// Normal tweet
		embed.Author = &discordgo.MessageEmbedAuthor{
			Name:    fmt.Sprintf("%s (@%s)", tweet.Author.Name, tweet.Author.ScreenName),
			URL:     fmt.Sprintf("https://x.com/%s", tweet.Author.ScreenName),
			IconURL: tweet.Author.AvatarURL,
		}
	}

	embed.Description = tweet.Text

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

	// Contract-address auto-detection. Scan the tweet text plus any expanded
	// URLs (a CA is sometimes only in a linked-out string) for EVM/Solana
	// addresses, resolve each to its DexScreener pair (exact chain + direct
	// chart link, Rick-bot style), and surface them as a copyable field +
	// chart buttons.
	caScanText := tweet.Text
	if len(tweet.URLs) > 0 {
		caScanText += " " + joinStrings(tweet.URLs, " ")
	}
	contracts := detectContracts(caScanText)
	for i := range contracts {
		contracts[i].ResolveDexScreener()
	}
	if len(contracts) > 0 {
		var caLines []string
		for _, c := range contracts {
			line := fmt.Sprintf("`%s`", c.Address)
			// Tag with symbol + resolved chain when we found a real pair.
			if c.Resolved() {
				tag := c.ResolvedChain
				if c.Symbol != "" {
					tag = "$" + c.Symbol + " · " + c.ResolvedChain
				}
				line += fmt.Sprintf("\n↳ [📈 %s](%s)", tag, c.ChartURL())
			} else {
				line += fmt.Sprintf(" · [🔍 search](%s)", c.ChartURL())
			}
			caLines = append(caLines, line)
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "💰 Contract Detected",
			Value:  joinStrings(caLines, "\n"),
			Inline: false,
		})
	}

	// Action buttons (Discord link components) — like the Waypoint-style cards.
	var buttons []discordgo.MessageComponent
	if tweet.TweetURL != "" {
		buttons = append(buttons, discordgo.Button{
			Label: "View Tweet",
			Style: discordgo.LinkButton,
			Emoji: &discordgo.ComponentEmoji{Name: "🐦"},
			URL:   tweet.TweetURL,
		})
	}
	if tweet.Author.ScreenName != "" {
		buttons = append(buttons, discordgo.Button{
			Label: "Profile",
			Style: discordgo.LinkButton,
			Emoji: &discordgo.ComponentEmoji{Name: "👤"},
			URL:   fmt.Sprintf("https://x.com/%s", tweet.Author.ScreenName),
		})
	}
	// One DexScreener chart button per detected contract. Discord caps an
	// ActionsRow at 5 buttons, and we already use up to 2 (View Tweet, Profile),
	// so add at most 3 chart buttons to stay within the limit.
	for idx, c := range contracts {
		if idx >= 3 {
			break
		}
		label := "Chart"
		switch {
		case c.Resolved() && c.Symbol != "":
			label = fmt.Sprintf("📈 $%s (%s)", c.Symbol, c.ResolvedChain)
		case c.Resolved():
			label = fmt.Sprintf("📈 Chart (%s)", c.ResolvedChain)
		case len(contracts) > 1:
			label = fmt.Sprintf("Chart %s", c.Short())
		}
		buttons = append(buttons, discordgo.Button{
			Label: label,
			Style: discordgo.LinkButton,
			Emoji: &discordgo.ComponentEmoji{Name: "📈"},
			URL:   c.ChartURL(),
		})
	}

	msg := &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
	}
	if len(buttons) > 0 {
		msg.Components = []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: buttons},
		}
	}

	_, err := db.session.ChannelMessageSendComplex(channelID, msg)
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
