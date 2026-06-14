package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// Dump raw notifications API response for analysis
func main() {
	authToken := os.Getenv("AUTH_TOKEN")
	ct0 := os.Getenv("CT0")

	if authToken == "" || ct0 == "" {
		fmt.Println("Usage: AUTH_TOKEN=xxx CT0=yyy go run dump_notifs.go")
		os.Exit(1)
	}

	url := "https://api.x.com/2/notifications/all.json?count=40&tweet_mode=extended"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		panic(err)
	}

	req.Header.Set("authorization", "Bearer AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs=1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA")
	req.Header.Set("x-csrf-token", ct0)
	req.Header.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", authToken, ct0))
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	req.Header.Set("x-twitter-active-user", "yes")
	req.Header.Set("x-twitter-auth-type", "OAuth2Session")
	req.Header.Set("x-twitter-client-language", "en")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Status: %d\n", resp.StatusCode)
	fmt.Printf("Body length: %d bytes\n", len(body))

	if resp.StatusCode != 200 {
		fmt.Println(string(body[:min(len(body), 500)]))
		return
	}

	// Parse and dump structure
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		panic(err)
	}

	// Dump globalObjects.notifications — this is where notification types live
	objs, _ := root["globalObjects"].(map[string]interface{})
	notifs, _ := objs["notifications"].(map[string]interface{})
	tweets, _ := objs["tweets"].(map[string]interface{})
	users, _ := objs["users"].(map[string]interface{})

	fmt.Printf("\n=== NOTIFICATIONS (%d) ===\n", len(notifs))
	for id, n := range notifs {
		nm, _ := n.(map[string]interface{})
		icon := mapGetStr(nm, "icon", "id")
		messageText := ""
		if msg, ok := nm["message"].(map[string]interface{}); ok {
			messageText = mapGetStr(msg, "text")
		}
		notifType := mapGetStr(nm, "type")
		
		// Get template info
		template, _ := nm["template"].(map[string]interface{})
		aggregateActions := template["aggregateUserActionsV1"]
		
		fmt.Printf("\n--- Notification: %s ---\n", id)
		fmt.Printf("  type: %s\n", notifType)
		fmt.Printf("  icon: %s\n", icon)
		fmt.Printf("  message: %s\n", messageText)
		fmt.Printf("  template keys: %v\n", getKeys(template))
		
		if aggregateActions != nil {
			agg, _ := aggregateActions.(map[string]interface{})
			fmt.Printf("  aggregateUserActionsV1 keys: %v\n", getKeys(agg))
			targetObjects := agg["targetObjects"]
			if targets, ok := targetObjects.([]interface{}); ok {
				fmt.Printf("  targetObjects count: %d\n", len(targets))
				for i, t := range targets {
					if tm, ok := t.(map[string]interface{}); ok {
						fmt.Printf("    [%d] keys: %v\n", i, getKeys(tm))
						if tw, ok := tm["tweet"].(map[string]interface{}); ok {
							tid := mapGetStr(tw, "id")
							fmt.Printf("    [%d] tweet id: %s\n", i, tid)
							if td, ok := tweets[tid].(map[string]interface{}); ok {
								fmt.Printf("    [%d] tweet text: %s\n", i, truncate(mapGetStr(td, "full_text"), 80))
								uid := mapGetStr(td, "user_id_str")
								if u, ok := users[uid].(map[string]interface{}); ok {
									fmt.Printf("    [%d] author: @%s\n", i, mapGetStr(u, "screen_name"))
								}
							}
						}
					}
				}
			}
			fromUsers := agg["fromUsers"]
			if fu, ok := fromUsers.([]interface{}); ok {
				fmt.Printf("  fromUsers count: %d\n", len(fu))
				for i, f := range fu {
					if fm, ok := f.(map[string]interface{}); ok {
						if u, ok := fm["user"].(map[string]interface{}); ok {
							fmt.Printf("    [%d] user: @%s (id: %s)\n", i, mapGetStr(u, "screen_name"), mapGetStr(u, "id"))
						}
					}
				}
			}
		} else {
			// No aggregateUserActionsV1 — dump full template
			fmt.Printf("  template (raw): %s\n", truncate(marshalJSON(template), 500))
		}
	}

	// Also dump timeline instructions structure for notification-to-tweet mapping
	fmt.Printf("\n=== TIMELINE INSTRUCTIONS ===\n")
	timeline, _ := root["timeline"].(map[string]interface{})
	instructions, _ := timeline["instructions"].([]interface{})
	fmt.Printf("instructions count: %d\n", len(instructions))
	for i, inst := range instructions {
		im, _ := inst.(map[string]interface{})
		fmt.Printf("  [%d] keys: %v\n", i, getKeys(im))
		
		// Check for addEntries or entries
		var entries []interface{}
		if ae, ok := im["addEntries"].(map[string]interface{}); ok {
			entries, _ = ae["entries"].([]interface{})
		}
		if entries == nil {
			entries, _ = im["entries"].([]interface{})
		}
		
		fmt.Printf("  [%d] entries count: %d\n", i, len(entries))
		for j, e := range entries {
			em, _ := e.(map[string]interface{})
			entryID := mapGetStr(em, "entryId")
			content, _ := em["content"].(map[string]interface{})
			contentKeys := getKeys(content)
			fmt.Printf("    [%d] entryId: %s, content keys: %v\n", j, entryID, contentKeys)
			
			// Check for notification references
			item, _ := content["item"].(map[string]interface{})
			if item != nil {
				itemContent, _ := item["content"].(map[string]interface{})
				if itemContent != nil {
					notif, _ := itemContent["notification"].(map[string]interface{})
					if notif != nil {
						fmt.Printf("    [%d] → notification id: %s\n", j, mapGetStr(notif, "id"))
					}
				}
			}
		}
	}
}

func mapGetStr(m map[string]interface{}, keys ...string) string {
	current := m
	for _, key := range keys[:len(keys)-1] {
		v, ok := current[key].(map[string]interface{})
		if !ok {
			return ""
		}
		current = v
	}
	v, _ := current[keys[len(keys)-1]].(string)
	return v
}

func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func marshalJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
