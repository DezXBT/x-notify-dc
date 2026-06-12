package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const bearerToken = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"

// User represents an X/Twitter user profile.
type User struct {
	ID              string `json:"id"`
	RestID          string `json:"restId"`
	Name            string `json:"name"`
	ScreenName      string `json:"screenName"`
	Description     string `json:"description"`
	FollowersCount  int    `json:"followersCount"`
	ProfileImageURL string `json:"profileImageUrl"`
}

// Tweet represents a single tweet.
type Tweet struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
	Author    struct {
		Name       string `json:"name"`
		ScreenName string `json:"screenName"`
		AvatarURL  string `json:"avatarUrl"`
	} `json:"author"`
	Metrics struct {
		Likes    int `json:"likes"`
		Retweets int `json:"retweets"`
		Replies  int `json:"replies"`
		Views    int `json:"views"`
	} `json:"metrics"`
	MediaURLs  []string `json:"mediaUrls"`
	URLs       []string `json:"urls"`
	IsRetweet  bool     `json:"isRetweet"`
	TweetURL   string   `json:"tweetUrl"`
}

// XClient talks to X's internal GraphQL + REST API using cookie auth.
type XClient struct {
	cookies   CookiePair
	client    *http.Client
	rateLimit map[string]rateInfo
	label     string
}

type rateInfo struct {
	remaining int
	reset     time.Time
}

func NewXClient(cookies CookiePair) *XClient {
	label := cookies.Label
	if label == "" {
		label = cookies.AuthToken[:min(8, len(cookies.AuthToken))]
	}
	return &XClient{
		cookies: cookies,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		rateLimit: make(map[string]rateInfo),
		label:     label,
	}
}

func (xc *XClient) getHeaders() http.Header {
	h := http.Header{}
	h.Set("authorization", "Bearer "+bearerToken)
	h.Set("x-twitter-auth-type", "OAuth2Session")
	h.Set("x-twitter-active-user", "yes")
	h.Set("x-csrf-token", xc.cookies.Ct0)
	h.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", xc.cookies.AuthToken, xc.cookies.Ct0))
	h.Set("content-type", "application/json")
	h.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	h.Set("x-twitter-client-language", "en")
	h.Set("accept", "*/*")
	h.Set("accept-language", "en-US,en;q=0.9")
	return h
}

func (xc *XClient) getFormHeaders() http.Header {
	h := http.Header{}
	h.Set("authorization", "Bearer "+bearerToken)
	h.Set("x-twitter-auth-type", "OAuth2Session")
	h.Set("x-twitter-active-user", "yes")
	h.Set("x-csrf-token", xc.cookies.Ct0)
	h.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", xc.cookies.AuthToken, xc.cookies.Ct0))
	h.Set("content-type", "application/x-www-form-urlencoded")
	h.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	h.Set("accept", "*/*")
	return h
}

func fallbackTransactionID() string {
	randPart := make([]byte, 32)
	if _, err := rand.Read(randPart); err != nil {
		binary.LittleEndian.PutUint64(randPart, uint64(time.Now().UnixNano()))
	}
	return base64.RawURLEncoding.EncodeToString(randPart)
}

// ────────────────────────────────────────────────────────
// GraphQL: UserByScreenName
// ────────────────────────────────────────────────────────

func (xc *XClient) GetUser(screenName string) (*User, error) {
	variables := map[string]interface{}{
		"screen_name":              screenName,
		"withSafetyModeUserFields": true,
	}
	data, err := xc.graphql("UserByScreenName", variables, false)
	if err != nil {
		return nil, err
	}
	userObj, ok := data["user"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing user in response for %s", screenName)
	}
	result, ok := userObj["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing user.result in response for %s", screenName)
	}
	return parseUser(result)
}

// ────────────────────────────────────────────────────────
// GraphQL: UserTweets
// ────────────────────────────────────────────────────────

func (xc *XClient) GetUserTweets(userID string, count int) ([]Tweet, error) {
	if count <= 0 {
		count = 5
	}
	variables := map[string]interface{}{
		"userId":                                 userID,
		"count":                                  count,
		"includePromotedContent":                 false,
		"withQuickPromoteEligibilityTweetFields": false,
		"withVoice":                              true,
		"withV2Timeline":                         true,
	}
	data, err := xc.graphql("UserTweets", variables, false)
	if err != nil {
		return nil, err
	}
	return parseTweets(data, userID)
}

// ────────────────────────────────────────────────────────
// REST: Follow / Unfollow
// ────────────────────────────────────────────────────────

const restBase = "https://api.x.com"

func (xc *XClient) Follow(screenName string) error {
	return xc.friendship("/1.1/friendships/create.json", screenName)
}

func (xc *XClient) Unfollow(screenName string) error {
	return xc.friendship("/1.1/friendships/destroy.json", screenName)
}

func (xc *XClient) friendship(path, screenName string) error {
	body := url.Values{"screen_name": {screenName}}
	req, err := http.NewRequest("POST", restBase+path, strings.NewReader(body.Encode()))
	if err != nil {
		return err
	}
	req.Header = xc.getFormHeaders()
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
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return fmt.Errorf("unauthorized (401) — cookie may be expired")
	}
	if resp.StatusCode == 403 {
		return fmt.Errorf("forbidden (403) — account may be suspended")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// ────────────────────────────────────────────────────────
// REST: Notification Settings
// ────────────────────────────────────────────────────────

// SetNotifications enables/disables notifications for a user.
// mode: "all" (device=true, all_replies=false),
//
//	"all+replies" (device=true, all_replies=true),
//	"off" (device=false)
func (xc *XClient) SetNotifications(screenName, mode string) error {
	body := url.Values{
		"screen_name": {screenName},
	}
	switch mode {
	case "all":
		body.Set("device", "true")
		body.Set("all_replies", "false")
	case "all+replies":
		body.Set("device", "true")
		body.Set("all_replies", "true")
	case "off":
		body.Set("device", "false")
	default:
		body.Set("device", "true")
		body.Set("all_replies", "false")
	}

	req, err := http.NewRequest("POST", restBase+"/1.1/friendships/update.json", strings.NewReader(body.Encode()))
	if err != nil {
		return err
	}
	req.Header = xc.getFormHeaders()
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
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return fmt.Errorf("unauthorized (401)")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// GetNotifications checks notification status for a user.
func (xc *XClient) GetNotifications(screenName string) (enabled bool, allReplies bool, err error) {
	u := fmt.Sprintf("%s/1.1/friendships/show.json?target_screen_name=%s", restBase, url.QueryEscape(screenName))
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return false, false, err
	}
	req.Header = xc.getFormHeaders()
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
		return false, false, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return false, false, fmt.Errorf("unauthorized (401)")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return false, false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, false, fmt.Errorf("decode: %w", err)
	}
	rel, ok := result["relationship"].(map[string]interface{})
	if !ok {
		return false, false, fmt.Errorf("missing relationship in response")
	}
	source, ok := rel["source"].(map[string]interface{})
	if !ok {
		return false, false, fmt.Errorf("missing source in relationship")
	}
	notifEnabled, _ := source["notifications_enabled"].(bool)
	allReps, _ := source["all_replies"].(bool)
	return notifEnabled, allReps, nil
}

// HealthCheck does a lightweight request to verify cookie is alive.
func (xc *XClient) HealthCheck() error {
	_, _, err := xc.GetNotifications("twitter")
	return err
}

// ────────────────────────────────────────────────────────
// GraphQL Engine
// ────────────────────────────────────────────────────────

var defaultFeatures = map[string]bool{
	"rweb_tipjar_consumption_enabled":                                         true,
	"responsive_web_graphql_exclude_directive_enabled":                        true,
	"verified_phone_label_enabled":                                            false,
	"creator_subscriptions_tweet_preview_api_enabled":                         true,
	"responsive_web_graphql_timeline_navigation_enabled":                      true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
	"communities_web_enable_tweet_community_results_fetch":                    true,
	"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
	"articles_preview_enabled":                                                true,
	"tweetypie_unmention_optimization_enabled":                                true,
	"responsive_web_edit_tweet_api_enabled":                                   true,
	"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
	"view_counts_everywhere_api_enabled":                                      true,
	"longform_notetweets_consumption_enabled":                                 true,
	"responsive_web_twitter_article_tweet_consumption_enabled":                true,
	"tweet_awards_web_tipping_enabled":                                        false,
	"creator_subscriptions_quote_tweet_preview_enabled":                       false,
	"freedom_of_speech_not_reach_fetch_enabled":                               true,
	"standardized_nudges_misinfo":                                             true,
	"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
	"rweb_video_timestamps_enabled":                                           true,
	"longform_notetweets_rich_text_read_enabled":                              true,
	"longform_notetweets_inline_media_enabled":                                true,
	"responsive_web_enhance_cards_enabled":                                    false,
	"responsive_web_twitter_article_notes_tab_enabled":                        true,
	"subscriptions_verification_info_verified_since_enabled":                  true,
	"subscriptions_verification_info_is_identity_verified_enabled":            true,
	"highlights_tweets_tab_ui_enabled":                                        true,
	"profile_label_improvements_pcf_label_post_embed_allowed":                 true,
	"hidden_profile_subscriptions_enabled":                                    true,
	"subscriptions_feature_can_gift_premium":                                  true,
	"responsive_web_grok_show_grok_translated_post":                           true,
	"responsive_web_grok_analyze_post_followups_enabled":                      true,
	"premium_content_api_read_enabled":                                        true,
	"responsive_web_grok_image_annotation_enabled":                            true,
	"responsive_web_grok_share_attachment_enabled":                            true,
	"responsive_web_grok_analysis_button_from_backend":                        true,
	"responsive_web_grok_analyze_button_fetch_trends_enabled":                 true,
	"rweb_video_screen_enabled":                                               true,
	"responsive_web_jetfuel_frame":                                            true,
	"rweb_cashtags_enabled":                                                   true,
	"responsive_web_profile_redirect_enabled":                                 true,
}

func (xc *XClient) graphql(operationName string, variables map[string]interface{}, usePost bool) (map[string]interface{}, error) {
	qid, ok := queryID(operationName)
	if !ok {
		return nil, fmt.Errorf("unknown operation: %s", operationName)
	}

	// Rate limit check
	if rl, exists := xc.rateLimit[operationName]; exists {
		if rl.remaining <= 0 && time.Now().Before(rl.reset) {
			wait := time.Until(rl.reset) + time.Second
			logWarn("[%s] rate limited on %s, waiting %s", xc.label, operationName, wait.Round(time.Second))
			time.Sleep(wait)
		}
	}

	// Jitter
	time.Sleep(time.Duration(50+randInt(200)) * time.Millisecond)

	variablesJSON, _ := json.Marshal(variables)
	featuresJSON, _ := json.Marshal(defaultFeatures)

	var req *http.Request
	var err error

	if usePost {
		body := map[string]interface{}{
			"features": defaultFeatures,
			"queryId":  qid,
		}
		bodyJSON, _ := json.Marshal(body)
		u := fmt.Sprintf("https://x.com/i/api/graphql/%s/%s?variables=%s",
			qid, operationName, url.QueryEscape(string(variablesJSON)))
		req, err = http.NewRequest("POST", u, strings.NewReader(string(bodyJSON)))
	} else {
		u := fmt.Sprintf("https://x.com/i/api/graphql/%s/%s?variables=%s&features=%s",
			qid, operationName, url.QueryEscape(string(variablesJSON)), url.QueryEscape(string(featuresJSON)))
		req, err = http.NewRequest("GET", u, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header = xc.getHeaders()

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
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// Update rate limits
	if rl := resp.Header.Get("x-rate-limit-remaining"); rl != "" {
		var remaining int
		fmt.Sscanf(rl, "%d", &remaining)
		if resetStr := resp.Header.Get("x-rate-limit-reset"); resetStr != "" {
			var resetUnix int64
			fmt.Sscanf(resetStr, "%d", &resetUnix)
			xc.rateLimit[operationName] = rateInfo{
				remaining: remaining,
				reset:     time.Unix(resetUnix, 0),
			}
		}
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("rate limited (429) on %s", operationName)
	}
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("unauthorized (401) — cookie may be expired")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d on %s: %s", resp.StatusCode, operationName, string(bodyBytes[:min(len(bodyBytes), 200)]))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if errs, ok := result["errors"].([]interface{}); ok && len(errs) > 0 {
		return nil, fmt.Errorf("GraphQL error: %v", errs[0])
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response structure")
	}
	return data, nil
}

// ────────────────────────────────────────────────────────
// Parsers
// ────────────────────────────────────────────────────────

func parseUser(result map[string]interface{}) (*User, error) {
	legacy, ok := result["legacy"].(map[string]interface{})
	if !ok || legacy == nil {
		return nil, fmt.Errorf("missing legacy data (suspended/deactivated?)")
	}

	user := &User{
		Name:            getString(legacy, "name"),
		ScreenName:      getString(legacy, "screen_name"),
		Description:     getString(legacy, "description"),
		FollowersCount:  getInt(legacy, "followers_count"),
		ProfileImageURL: getString(legacy, "profile_image_url_https"),
	}
	if id, ok := result["rest_id"].(string); ok {
		user.RestID = id
	}
	if id, ok := result["id"].(string); ok {
		user.ID = id
	}
	return user, nil
}

func parseTweets(data map[string]interface{}, targetUserID string) ([]Tweet, error) {
	// Navigate timeline structure
	timeline := data
	if u, ok := timeline["user"].(map[string]interface{}); ok {
		if r, ok := u["result"].(map[string]interface{}); ok {
			if t, ok := r["timeline"].(map[string]interface{}); ok {
				if t2, ok := t["timeline"].(map[string]interface{}); ok {
					timeline = t2
				}
			}
		}
	}

	instructions, _ := timeline["instructions"].([]interface{})
	if len(instructions) == 0 {
		return nil, nil
	}

	var tweets []Tweet
	for _, inst := range instructions {
		m, ok := inst.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] != "TimelineAddEntries" {
			continue
		}
		entries, _ := m["entries"].([]interface{})
		for _, entry := range entries {
			e, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			content, _ := e["content"].(map[string]interface{})
			if content == nil {
				continue
			}
			// Skip cursors
			if ct, _ := content["cursorType"].(string); ct != "" {
				continue
			}
			itemContent, _ := content["itemContent"].(map[string]interface{})
			if itemContent == nil {
				continue
			}
			if itemContent["itemType"] == "TimelineTimelineCursor" {
				continue
			}
			tweetResults, _ := itemContent["tweet_results"].(map[string]interface{})
			if tweetResults == nil {
				continue
			}
			result, _ := tweetResults["result"].(map[string]interface{})
			if result == nil {
				continue
			}
			tweet := parseSingleTweet(result)
			if tweet != nil {
				tweets = append(tweets, *tweet)
			}
		}
	}
	return tweets, nil
}

func parseSingleTweet(result map[string]interface{}) *Tweet {
	// Handle tombstones
	if result["__typename"] == "TweetTombstone" {
		return nil
	}

	legacy, ok := result["legacy"].(map[string]interface{})
	if !ok || legacy == nil {
		return nil
	}

	tweetID := getString(legacy, "id_str")
	if tweetID == "" {
		if rid, ok := result["rest_id"].(string); ok {
			tweetID = rid
		}
	}

	text := getString(legacy, "full_text")
	if text == "" {
		text = getString(legacy, "text")
	}

	// Get author info from core — X puts screen_name in result.core, not result.legacy
	var authorName, authorHandle, avatarURL string
	if core, ok := result["core"].(map[string]interface{}); ok {
		if ur, ok := core["user_results"].(map[string]interface{}); ok {
			if r, ok := ur["result"].(map[string]interface{}); ok {
				// Try result.core first (new X format)
				if uc, ok := r["core"].(map[string]interface{}); ok {
					authorName = getString(uc, "name")
					authorHandle = getString(uc, "screen_name")
				}
				// Fallback to result.legacy
				if authorHandle == "" {
					if l, ok := r["legacy"].(map[string]interface{}); ok {
						authorName = getString(l, "name")
						authorHandle = getString(l, "screen_name")
					}
				}
				// Avatar from avatar.image_url or legacy.profile_image_url_https
				if avatar, ok := r["avatar"].(map[string]interface{}); ok {
					avatarURL = getString(avatar, "image_url")
				}
				if avatarURL == "" {
					if l, ok := r["legacy"].(map[string]interface{}); ok {
						avatarURL = getString(l, "profile_image_url_https")
					}
				}
			}
		}
	}

	// Check if retweet: text prefix OR retweeted_status_result in legacy
	isRT := strings.HasPrefix(text, "RT @")
	if !isRT {
		if _, ok := legacy["retweeted_status_result"]; ok {
			isRT = true
		}
	}

	// Metrics
	metrics := struct {
		Likes    int `json:"likes"`
		Retweets int `json:"retweets"`
		Replies  int `json:"replies"`
		Views    int `json:"views"`
	}{
		Likes:    getInt(legacy, "favorite_count"),
		Retweets: getInt(legacy, "retweet_count"),
		Replies:  getInt(legacy, "reply_count"),
	}
	// Views from views.count
	if views, ok := result["views"].(map[string]interface{}); ok {
		metrics.Views = getInt(views, "count")
	}

	// Media URLs
	var mediaURLs []string
	if entities, ok := legacy["entities"].(map[string]interface{}); ok {
		if media, ok := entities["media"].([]interface{}); ok {
			for _, m := range media {
				if mm, ok := m.(map[string]interface{}); ok {
					if u := getString(mm, "media_url_https"); u != "" {
						mediaURLs = append(mediaURLs, u)
					}
				}
			}
		}
	}

	// External URLs
	var urls []string
	if entities, ok := legacy["entities"].(map[string]interface{}); ok {
		if urlList, ok := entities["urls"].([]interface{}); ok {
			for _, u := range urlList {
				if uu, ok := u.(map[string]interface{}); ok {
					if expanded := getString(uu, "expanded_url"); expanded != "" {
						urls = append(urls, expanded)
					}
				}
			}
		}
	}

	createdAt := getString(legacy, "created_at")
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
		Metrics:    metrics,
		MediaURLs:  mediaURLs,
		URLs:       urls,
		IsRetweet:  isRT,
		TweetURL:   tweetURL,
	}
}

// ────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	// Handle count fields that might be float64
	if v, ok := m[key].(float64); ok {
		return fmt.Sprintf("%.0f", v)
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
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

func randInt(max int) int {
	if max <= 0 {
		return 0
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return 0
	}
	return int(binary.BigEndian.Uint32(b) % uint32(max))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
