# x-notify-dc — Discord X/Twitter Notification Bot

## Overview

Discord bot yang memungkinkan user menambahkan akun X/Twitter via slash command. Bot akan follow + enable notifications, lalu forward setiap postingan baru ke Discord channel.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                   Discord Bot                        │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐  │
│  │ /add     │  │ /list    │  │ /remove           │  │
│  │ /remove  │  │ /status  │  │ /settings         │  │
│  └────┬─────┘  └──────────┘  └───────────────────┘  │
│       │                                              │
│  ┌────▼─────────────────────────────────────────┐    │
│  │           Watch Manager                       │    │
│  │  - manage watch_list.json (per-user accounts) │    │
│  │  - follow/unfollow X accounts                 │    │
│  │  - enable/disable notifications               │    │
│  └────┬──────────────────────────────────────────┘    │
│       │                                              │
│  ┌────▼──────────────────────────────────────────┐    │
│  │           Poll Engine                          │    │
│  │  - poll UserTweets per watched account         │    │
│  │  - detect new tweets (dedup via seen IDs)      │    │
│  │  - send embed to configured Discord channel    │    │
│  └────┬──────────────────────────────────────────┘    │
│       │                                              │
│  ┌────▼──────────────────────────────────────────┐    │
│  │           X Client (reused from X-Tracker)     │    │
│  │  - GraphQL: UserTweets, UserByScreenName       │    │
│  │  - REST: friendships/create, friendships/update │    │
│  │  - Cookie auth + X-Client-Transaction-Id       │    │
│  └───────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
          │                    │
          ▼                    ▼
    ┌──────────┐        ┌──────────┐
    │  X API   │        │ Discord  │
    │ (poll)   │        │ (embed)  │
    └──────────┘        └──────────┘
```

## Tech Stack

| Component | Tech |
|-----------|------|
| Language | Go (reuse X-Tracker-Bot patterns) |
| Discord | `discordgo` library (slash commands + embeds) |
| X API | GraphQL (UserTweets, UserByScreenName) + REST (friendships) |
| Storage | JSON files (watch_list.json, seen_tweets.json) |
| Auth | Cookie-based (auth_token + ct0) |
| Transaction ID | Reuse X-Tracker-Bot's transaction.go + queryid.go |

## Discord Slash Commands

### `/add <handle> [channel]`
- Follow akun X, enable notifications
- Tambah ke watch list
- Reply dengan embed sukses (nama, followers, bio)
- `channel` optional: target channel untuk notif (default: channel command dijalankan)

### `/remove <handle>`
- Unfollow + disable notifications
- Hapus dari watch list
- Reply konfirmasi

### `/list`
- Show semua akun yang di-watch
- Format: embed dengan list handle + followers + status

### `/settings <handle> [mode]`
- `all` — semua postingan (default)
- `all+replies` — postingan + balasan
- `off` — matikan notif tapi tetap watch

### `/status`
- Bot health: uptime, total watched, last poll time, rate limit status

## File Structure

```
x-notify-dc/
├── main.go              # entrypoint, bot init, graceful shutdown
├── config.go            # config.yaml parsing + defaults
├── config.yaml          # runtime config (tokens, cookies, channels)
├── discord.go           # slash command handlers + embed builders
├── twitter.go           # X API client (GraphQL + REST)
├── queryid.go           # GraphQL query IDs (copied from X-Tracker)
├── transaction.go       # X-Client-Transaction-Id generator
├── poller.go            # poll loop: check UserTweets, detect new, send
├── watch.go             # watch list CRUD (add/remove/list)
├── state.go             # seen tweet IDs + persistence
├── notify.go            # follow + enable notifications via REST
├── logger.go            # structured logging
├── go.mod
├── go.sum
└── README.md
```

## Data Models

```go
// WatchEntry represents one watched account
type WatchEntry struct {
    Handle          string    `json:"handle"`
    UserID          string    `json:"user_id"`
    AddedBy         string    `json:"added_by"`      // Discord user ID
    ChannelID       string    `json:"channel_id"`     // Target Discord channel
    NotifyMode      string    `json:"notify_mode"`    // "all", "all+replies", "off"
    AddedAt         time.Time `json:"added_at"`
    FollowersCount  int       `json:"followers_count"`
    ProfileImageURL string    `json:"profile_image_url"`
}

// SeenState tracks last-seen tweet IDs per account
type SeenState struct {
    LastTweetID map[string]string `json:"last_tweet_id"` // handle -> last tweet ID
    UpdatedAt   time.Time         `json:"updated_at"`
}
```

## Config (config.yaml)

```yaml
discord:
  bot_token: ""           # Discord bot token
  guild_id: ""            # Optional: restrict to one server

twitter:
  cookies:
    - auth_token: ""
      ct0: ""

tracking:
  poll_interval: "60s"    # How often to check for new tweets
  tweets_per_check: 5     # How many recent tweets to fetch per account

logging:
  level: "info"
  timezone: "Asia/Jakarta"
```

## Poll Flow

1. Every `poll_interval` (default 60s):
   - For each watched account:
     - Fetch `UserTweets(userID, count=5)` via GraphQL
     - Compare tweet IDs with `seen_state.last_tweet_id[handle]`
     - If new tweets found (newer than last seen):
       - Build Discord embed (author, text, media, metrics, link)
       - Send to target channel
       - Update `last_tweet_id`
   - Sleep until next interval

2. First run per account: seed `last_tweet_id` silently (no alerts)

## Discord Embed Format

```
🐦 New tweet from @handle

> tweet text here...

❤️ 1.2K  🔁 345  💬 89  👁️ 45K
```

Fields: tweet text, engagement metrics, direct link
Thumbnail: author's profile image
Footer: timestamp (WIB)

## Implementation Phases

### Phase 1: Core (MVP)
- [x] Plan written
- [ ] Go project init + config
- [ ] X Client (reuse twitter.go, queryid.go, transaction.go)
- [ ] Discord bot setup (discordgo, slash commands)
- [ ] `/add` command → follow + enable notifications
- [ ] `/remove` command → unfollow + disable notifications
- [ ] `/list` command → show watched accounts
- [ ] Poll engine → detect new tweets
- [ ] Discord embed sender

### Phase 2: Polish
- [ ] `/settings` per-account notification mode
- [ ] `/status` health command
- [ ] Rate limit handling + client rotation
- [ ] Graceful shutdown + state persistence
- [ ] systemd service file

### Phase 3: Advanced
- [ ] Multi-user support (per Discord user watch lists)
- [ ] Media detection (images, videos, articles)
- [ ] Thread detection (unrolled replies)
- [ ] Category filtering (only notify for certain tweet types)

## Key Differences from X-Tracker-Bot

| | X-Tracker-Bot | x-notify-dc |
|---|---|---|
| Purpose | Detect who KOLs follow | Detect new tweets |
| Input | Static watch list (file) | Dynamic (Discord commands) |
| API | UserFollowing (GraphQL) | UserTweets (GraphQL) |
| Notification | Webhook (HTTP) | Bot embed (discordgo) |
| Interaction | None (passive) | Slash commands (active) |
| Auth | Multiple cookies (rotation) | Single cookie (simpler) |

## Risks & Mitigations

1. **Rate limits**: X has ~50 req/15min on UserTweets → with 60s interval, max ~40 accounts per bot instance. Mitigation: multiple cookie pairs, longer intervals for many accounts.

2. **Cookie expiry**: auth_token expires periodically → bot logs warning, user needs to update config. Future: auto-refresh mechanism.

3. **Tweet detection gap**: If bot is down during a tweet burst, missed tweets won't be recovered. Mitigation: state persistence, startup warmup.

4. **Discord bot permissions**: Need `applications.commands` scope + `Send Messages` + `Embed Links` in target channels.

---

**Status: Awaiting review before implementation.**
