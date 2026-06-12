# x-notify-dc

Discord bot that watches X/Twitter accounts and forwards new tweets to Discord channels.

## Features

- `/add <handle>` — Follow an X account + enable notifications + start watching
- `/remove <handle>` — Unfollow + stop watching
- `/list` — Show all watched accounts
- `/settings <handle> <mode>` — Change notification mode (all / all+replies / off)
- `/status` — Bot health, uptime, stats
- Multi-cookie rotation (survives account bans)
- Auto re-sync when cookies change
- Config hot-reload (cookie changes detected automatically)

## Setup

1. Copy config:
```bash
cp config.example.yaml config.yaml
```

2. Fill in:
- `discord.bot_token` — Discord bot token from Developer Portal
- `twitter.cookies[0].auth_token` — X auth_token cookie
- `twitter.cookies[0].ct0` — X ct0 cookie

3. Build & run:
```bash
go build -o x-notify-dc .
./x-notify-dc -config config.yaml
```

## How It Works

1. User runs `/add @handle` in Discord
2. Bot follows the account + enables "All Posts" notifications via X API
3. Poller checks `UserTweets` every 60s for new tweets
4. New tweets → Discord embed in the target channel

## Architecture

- **X Client** — GraphQL (UserTweets, UserByScreenName) + REST (friendships)
- **Discord Bot** — discordgo with slash commands
- **Poller** — Background goroutine with dedup via seen tweet IDs
- **Watch Manager** — JSON-backed persistent watch list
- **State** — Cookie hash tracking for auto re-sync on account swap

## Cookie Rotation

Bot supports multiple X cookies. If one fails:
1. Auto-rotates to next cookie
2. Re-syncs all follows + notifications with new account
3. Sends health alert if configured

When you swap cookies:
1. Update `config.yaml`
2. Bot detects change → auto re-syncs all accounts

## License

Private — DezXBT
