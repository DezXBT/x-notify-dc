<!--
x-notify-dc — real-time X/Twitter notification bot for Discord.
Keywords: Twitter notification bot Discord, X monitor Discord, tweet tracker Discord bot, real-time Twitter alerts Discord, X webhook Discord, Twitter stream Discord bot, Go Discord bot, multi-cookie Twitter bot, GraphQL Twitter monitor, X/Twitter notification pipeline.
-->

# x-notify-dc — Real-Time X/Twitter → Discord Notification Pipeline

> **Zero-latency X/Twitter alerts in Discord.**  
> WebSocket push + REST polling hybrid with multi-cookie rotation, device_follow batching, and Discord button components.  
> Built for production — survives cookie bans, auto-resyncs, and stays lean at ~14 MB RAM.

<img alt="Language: Go" src="https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white">
<img alt="Platform" src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey">
<img alt="License: Private" src="https://img.shields.io/badge/license-Private-red">
<img alt="RAM usage" src="https://img.shields.io/badge/RAM-~14MB-blue">

---

## Table of Contents

- [What Is x-notify-dc?](#what-is-x-notify-dc)
- [Key Features](#key-features)
- [What You Need](#what-you-need)
- [Installation Guide](#installation-guide)
- [Configuration Reference](#configuration-reference)
- [Slash Commands](#slash-commands)
- [How It Works](#how-it-works)
- [Running 24/7](#running-247)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)
- [Tech Stack](#tech-stack)
- [License](#license)

---

## What Is x-notify-dc?

x-notify-dc is a production-grade Discord bot that watches X/Twitter accounts and forwards new tweets to Discord channels in real time. It uses X's native **WebSocket live_pipeline** for instant push notifications, backed by **REST device_follow.json** as a safety net — you get sub-second alerts when a watched account posts.

Why not just poll every N seconds? WebSocket push means you catch tweets the moment they're published, not minutes later. The adaptive polling fallback ensures nothing is missed even if the WebSocket disconnects.

| Use Case | How It Helps |
|----------|--------------|
| NFT/Token alpha | Catch project announcements before they trend |
| News monitoring | Track journalist accounts in real-time |
| Competitor watch | Monitor competitor accounts silently |
| Personal notifications | Get alerted when specific accounts post |

---

## Key Features

| Feature | Description |
|---------|-------------|
| ⚡ **Real-Time WebSocket** | X's live_pipeline push → instant tweet delivery |
| 🛡️ **Multi-Cookie Rotation** | Survives account bans — auto-rotates to next cookie |
| 🔄 **Auto Re-Sync** | Detects cookie changes in config → re-follows + re-enables notifications |
| 🔘 **Discord Buttons** | Tweet embeds include 🐦 View Tweet and 👤 Profile buttons |
| 📊 **Reply Detection** | GraphQL SearchTimeline for `all+replies` mode |
| 📡 **Adaptive Polling** | 5s active, 30s idle — fast when needed, gentle when quiet |
| 🔥 **Config Hot-Reload** | Edit `config.yaml` → bot picks up changes without restart |
| 🏥 **Health Checks** | Periodic cookie validation, `/cookie health` command |
| 🎛️ **Per-Account Modes** | All Posts, All Posts + Replies, or Off per watched account |

---

## What You Need

- [ ] **Discord Bot Token** — from [Discord Developer Portal](https://discord.com/developers/applications)
- [ ] **X/Twitter Cookie** — `auth_token` + `ct0` from your browser's DevTools
- [ ] **Go 1.24+** — for building from source
- [ ] **Linux/macOS/Windows** — any platform Go supports

> ⚠️ **Cookie Security:** Your X cookies grant full account access. Never commit them to git. Use environment variables or keep `config.yaml` in `.gitignore`.

---

## Installation Guide

### Step 1 — Clone the repo

```bash
git clone https://github.com/DezXBT/x-notify-dc.git
cd x-notify-dc
```

### Step 2 — Copy and edit config

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` — fill in your Discord bot token and X cookie:

```yaml
discord:
  bot_token: "your-discord-bot-token"
  default_channel: "your-channel-id"

twitter:
  cookies:
    - auth_token: "your-x-auth-token"
      ct0: "your-x-ct0"
      label: "main"
```

### Step 3 — Build

```bash
go build -o x-notify-dc .
```

### Step 4 — Invite the bot to your server

1. Go to Discord Developer Portal → your app → OAuth2 → URL Generator
2. Select scope: `bot` + `applications.commands`
3. Permissions: `Send Messages`, `Embed Links`, `Use Slash Commands`
4. Paste the generated URL in your browser, select your server

### Step 5 — Run

```bash
./x-notify-dc -config config.yaml
```

---

## Configuration Reference

```yaml
discord:
  bot_token: "${DISCORD_BOT_TOKEN}"   # Discord bot token
  default_channel: "1514976399419510865"  # Default notification channel ID
  guild_id: ""                        # Optional: guild ID for instant slash commands

twitter:
  cookies:
    - auth_token: "${AUTH_TOKEN}"     # X auth_token cookie
      ct0: "${CT0}"                   # X ct0 cookie
      label: "main"                   # Human-readable label for this cookie
  health_check_interval: "5m"         # How often to validate cookies

tracking:
  poll_interval: "5s"                 # Fast polling interval (active mode)
  idle_poll_interval: "30s"           # Slow polling interval (idle mode)
  idle_threshold: "5m"                # Time without activity before idle mode
  tweets_per_check: 5                 # Max tweets fetched per account per poll

logging:
  level: "info"                       # debug, info, warn, error
  timezone: "Asia/Jakarta"            # IANA timezone for timestamps
```

---

## Slash Commands

| Command | Description |
|---------|-------------|
| `/add <handle> [channel]` | Start watching an X/Twitter account. Optional: target channel override. |
| `/remove <handle>` | Stop watching an account. |
| `/list` | Show all watched accounts with their notification modes. |
| `/settings <handle> <mode>` | Change mode: `all`, `all+replies`, or `off`. |
| `/status` | Bot health, uptime, poll stats, cookie count. |
| `/cookie add <auth_token> <ct0> [label]` | Add a new X cookie. Auto-tests validity first. |
| `/cookie remove <label>` | Remove a cookie by label. |
| `/cookie list` | Show all cookies (masked). |
| `/cookie health` | Test all cookies — green/yellow/red status. |
| `/setup <channel>` | Set the default notification channel. |

---

## How It Works

```
┌──────────┐     WebSocket Push     ┌──────────────┐
│  X/Twitter │───────▶──────────────▶│  live_pipeline │
│  Servers   │                       │  (real-time)   │
└──────────┘                       └──────┬───────┘
                                         │ push event
┌──────────┐     REST Poll (safety)  ┌───▼────────┐
│  X REST   │◀──────────────────────▶│   Poller     │
│  API v1.1 │   device_follow.json   │  (hybrid)    │
└──────────┘                        └──────┬───────┘
                                          │ matched tweet
┌──────────┐     Discord API         ┌───▼────────┐
│  Discord  │◀──────────────────────▶│  Discord Bot │
│  Servers  │   Embeds + Buttons     │  (discordgo) │
└──────────┘                        └──────────────┘
```

**Flow:**
1. WebSocket `live_pipeline` subscribes to notifications for all cookie accounts
2. On push → immediate REST poll via `device_follow.json` (efficient single-call batch)
3. For `all+replies` mode → supplementary `SearchTimeline` GraphQL scan
4. New tweets matched against watch list → deduplicated via SeenState
5. Discord embed sent with 🐦 View Tweet + 👤 Profile buttons

**State Files:**
| File | Purpose |
|------|---------|
| `data/watch.json` | Watched accounts + notification modes |
| `data/state.json` | Last-seen tweet IDs, reply IDs, notif cursor, cookie hash |

---

## Running 24/7

### Screen (recommended)

```bash
screen -dmS x-notify ./x-notify-dc -config config.yaml
```

### systemd

```ini
[Unit]
Description=x-notify-dc
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/root/x-notify-dc
ExecStart=/root/x-notify-dc/x-notify-dc -config /root/x-notify-dc/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `401 Unauthorized` | Cookie expired. Update `auth_token` + `ct0` in config.yaml. |
| `403 Forbidden` on follow | Account may be locked or rate-limited. Check X manually. |
| No tweets appearing | Run `/list` to verify account is watched. Check `/status` for errors. |
| WebSocket disconnect | Auto-falls back to polling. Check logs for persistent WS errors. |
| `device_follow` returns empty | Ensure cookie accounts follow the target + bell is enabled. Run `/cookie health`. |
| Discord slash commands not showing | Bot needs `applications.commands` scope. Re-invite with correct permissions. |
| Buttons not appearing | Requires `Embed Links` permission. Verify bot permissions in server settings. |

---

## FAQ

**How many accounts can I watch?**
No hard limit. Tested with dozens. Each account adds ~200ms to the per-account fallback scan.

**Can I use multiple X cookies?**
Yes. Add multiple entries under `twitter.cookies`. Bot rotates on failure and auto-resyncs.

**Does this use the official X API?**
No — it uses X's internal GraphQL and REST endpoints (the same ones the web app uses). No API key required.

**What's the difference between `all` and `all+replies`?**
`all` = main tweets only (via `device_follow`). `all+replies` = main tweets + replies (via `SearchTimeline` supplementary scan).

**Can I add cookies without restarting?**
Yes — use `/cookie add` in Discord. The bot adds the cookie to `config.yaml` and the running instance.

---

## Tech Stack

- **Go 1.24+** — single binary, no runtime dependencies
- **discordgo** — Discord Gateway + REST
- **gorilla/websocket** — X WebSocket live_pipeline
- **X Internal API** — GraphQL (UserTweets, SearchTimeline, UserByScreenName, CreateFriendship) + REST v1.1 (friendships, device_follow)

---

## License

Private — DezXBT