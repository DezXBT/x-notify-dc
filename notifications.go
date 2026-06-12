package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// NotificationPage holds parsed results from /2/notifications/all.json
type NotificationPage struct {
	Tweets     []Tweet // resolved tweets from notifications
	NotifIDs   []string
	NextCursor string
}

// FetchNotifications hits X's notifications endpoint — the same endpoint the
// X mobile/web app uses. Returns tweets that appeared in notifications tab.
// tab: "all", "mentions", or "verified"
func (xc *XClient) FetchNotifications(tab string, count int, cursor string) (*NotificationPage, error) {
	if tab == "" {
		tab = "all"
	}
	if count <= 0 {
		count = 40
	}

	params := url.Values{
		"count":      {strconv.Itoa(count)},
		"tweet_mode": {"extended"},
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	reqURL := fmt.Sprintf("%s/2/notifications/%s.json?%s", restBase, tab, params.Encode())
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header = xc.getFormHeaders()
	req.Header.Set("content-type", "application/json")

	if TxGen != nil {
		if txID := Generate(req.Method, req.URL.Path); txID != "" {
			req.Header.Set("x-client-transaction-id", txID)
		}
	}
	if req.Header.Get("x-client-transaction-id") == "" {
		req.Header.Set("x-client-transaction-id", fallbackTransactionID())
	}

	resp, err := xc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("unauthorized (401)")
	}
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("rate limited (429)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	return parseNotificationsPage(body)
}

// parseNotificationsPage parses the legacy globalObjects + timeline structure.
func parseNotificationsPage(body []byte) (*NotificationPage, error) {
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	objs := mapGet(root, "globalObjects")
	tweets := mapGet(objs, "tweets")
	users := mapGet(objs, "users")

	page := &NotificationPage{}

	// Walk timeline.instructions[].addEntries().entries[]
	instructions := sliceGet(mapGet(root, "timeline"), "instructions")
	for _, inst := range instructions {
		instMap, _ := inst.(map[string]interface{})
		if instMap == nil {
			continue
		}

		// Try addEntries path first
		entries := sliceGet(mapGet(instMap, "addEntries"), "entries")
		if entries == nil {
			// Some responses use replaceEntry or type-specific keys
			entries = sliceGet(instMap, "entries")
		}

		for _, e := range entries {
			entry, _ := e.(map[string]interface{})
			if entry == nil {
				continue
			}
			content := mapGet(entry, "content")

			// Cursor — pagination
			if cur := mapGet(mapGet(content, "operation"), "cursor"); cur != nil {
				if mapGetString(cur, "cursorType") == "Bottom" {
					page.NextCursor = mapGetString(cur, "value")
				}
				continue
			}

			// Notification item (text-only, e.g. "X liked your post")
			if n := mapGet(mapGet(mapGet(content, "item"), "content"), "notification"); n != nil {
				page.NotifIDs = append(page.NotifIDs, mapGetString(n, "id"))
				continue
			}

			// Tweet item — the actual tweet in notifications
			tw := mapGet(mapGet(mapGet(content, "item"), "content"), "tweet")
			if tw == nil {
				// Also try direct tweet reference at entry level
				tw = mapGet(content, "tweet")
			}
			if tw != nil {
				tweetID := mapGetString(tw, "id")
				td := mapGet(tweets, tweetID)
				if td == nil {
					td = tw
				}

				// Inject user from globalObjects
				if _, ok := td["user"]; !ok {
					uid := mapGetString(td, "user_id_str")
					if u := mapGet(users, uid); u != nil {
						td["user"] = u
					}
				}

				tweet := parseV11Tweet(td)
				if tweet != nil {
					page.Tweets = append(page.Tweets, *tweet)
					page.NotifIDs = append(page.NotifIDs, tweetID)
				}
			}
		}
	}

	return page, nil
}

// parseV11Tweet parses a v1.1 format tweet from globalObjects.tweets.
func parseV11Tweet(td map[string]interface{}) *Tweet {
	if td == nil {
		return nil
	}

	tweetID := mapGetString(td, "id_str")
	if tweetID == "" {
		tweetID = mapGetString(td, "id")
	}
	text := mapGetString(td, "full_text")
	if text == "" {
		text = mapGetString(td, "text")
	}
	if text == "" {
		return nil
	}

	// Author
	var authorName, authorHandle, avatarURL string
	if u := mapGet(td, "user"); u != nil {
		authorName = mapGetString(u, "name")
		authorHandle = mapGetString(u, "screen_name")
		avatarURL = mapGetString(u, "profile_image_url_https")
	}

	isRT := false
	if _, ok := td["retweeted_status_result"]; ok {
		isRT = true
	}
	if len(text) > 3 && text[:3] == "RT " {
		isRT = true
	}

	// Metrics
	metrics := struct {
		Likes    int `json:"likes"`
		Retweets int `json:"retweets"`
		Replies  int `json:"replies"`
		Views    int `json:"views"`
	}{
		Likes:    mapGetInt(td, "favorite_count"),
		Retweets: mapGetInt(td, "retweet_count"),
		Replies:  mapGetInt(td, "reply_count"),
	}
	if views := mapGet(td, "ext_views"); views != nil {
		metrics.Views = mapGetInt(views, "count")
	}

	// Media
	var mediaURLs []string
	if entities := mapGet(td, "entities"); entities != nil {
		for _, m := range sliceGet(entities, "media") {
			if mm, ok := m.(map[string]interface{}); ok {
				if u := mapGetString(mm, "media_url_https"); u != "" {
					mediaURLs = append(mediaURLs, u)
				}
			}
		}
	}
	if extEntities := mapGet(td, "extended_entities"); extEntities != nil {
		for _, m := range sliceGet(extEntities, "media") {
			if mm, ok := m.(map[string]interface{}); ok {
				if u := mapGetString(mm, "media_url_https"); u != "" {
					if !containsStr(mediaURLs, u) {
						mediaURLs = append(mediaURLs, u)
					}
				}
			}
		}
	}

	createdAt := mapGetString(td, "created_at")
	tweetURL := fmt.Sprintf("https://x.com/%s/status/%s", authorHandle, tweetID)

	return &Tweet{
		ID:        tweetID,
		Text:      text,
		CreatedAt: createdAt,
		Author: struct {
			Name       string `json:"name"`
			ScreenName string `json:"screenName"`
			AvatarURL  string `json:"avatarUrl"`
		}{
			Name:       authorName,
			ScreenName: authorHandle,
			AvatarURL:  avatarURL,
		},
		Metrics:   metrics,
		MediaURLs: mediaURLs,
		IsRetweet: isRT,
		TweetURL:  tweetURL,
	}
}

// ────────────────────────────────────────────────────────
// JSON helpers (type-safe wrappers)
// ────────────────────────────────────────────────────────

func mapGet(m map[string]interface{}, key string) map[string]interface{} {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(map[string]interface{}); ok {
		return v
	}
	return nil
}

func mapGetString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func mapGetInt(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

func sliceGet(m map[string]interface{}, key string) []interface{} {
	if m == nil {
		return nil
	}
	if v, ok := m[key].([]interface{}); ok {
		return v
	}
	return nil
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
