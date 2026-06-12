package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// WatchEntry represents one watched X account.
type WatchEntry struct {
	Handle          string    `json:"handle"`
	UserID          string    `json:"user_id"`
	AddedBy         string    `json:"added_by"`       // Discord user ID
	ChannelID       string    `json:"channel_id"`      // Target Discord channel
	GuildID         string    `json:"guild_id"`        // Discord guild
	NotifyMode      string    `json:"notify_mode"`     // "all", "all+replies", "off"
	AddedAt         time.Time `json:"added_at"`
	FollowersCount  int       `json:"followers_count"`
	ProfileImageURL string    `json:"profile_image_url"`
	Name            string    `json:"name"`            // Display name
}

// WatchManager manages the persistent watch list.
type WatchManager struct {
	mu      sync.RWMutex
	entries []WatchEntry
	path    string
}

func NewWatchManager(dataDir string) (*WatchManager, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "watch_list.json")
	wm := &WatchManager{path: path}
	wm.load()
	return wm, nil
}

func (wm *WatchManager) load() {
	data, err := os.ReadFile(wm.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &wm.entries)
}

func (wm *WatchManager) save() error {
	data, err := json.MarshalIndent(wm.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(wm.path, data, 0644)
}

// Add adds a new watch entry. Returns false if already exists.
func (wm *WatchManager) Add(entry WatchEntry) bool {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	key := strings.ToLower(entry.Handle)
	for _, e := range wm.entries {
		if strings.ToLower(e.Handle) == key {
			return false // already watching
		}
	}
	wm.entries = append(wm.entries, entry)
	_ = wm.save()
	return true
}

// Remove removes a watch entry by handle. Returns true if found.
func (wm *WatchManager) Remove(handle string) bool {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	key := strings.ToLower(handle)
	for i, e := range wm.entries {
		if strings.ToLower(e.Handle) == key {
			wm.entries = append(wm.entries[:i], wm.entries[i+1:]...)
			_ = wm.save()
			return true
		}
	}
	return false
}

// Get returns a watch entry by handle (case-insensitive).
func (wm *WatchManager) Get(handle string) (WatchEntry, bool) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	key := strings.ToLower(handle)
	for _, e := range wm.entries {
		if strings.ToLower(e.Handle) == key {
			return e, true
		}
	}
	return WatchEntry{}, false
}

// GetAll returns all watch entries sorted by AddedAt.
func (wm *WatchManager) GetAll() []WatchEntry {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	out := make([]WatchEntry, len(wm.entries))
	copy(out, wm.entries)
	sort.Slice(out, func(i, j int) bool {
		return out[i].AddedAt.Before(out[j].AddedAt)
	})
	return out
}

// UpdateNotifyMode updates the notification mode for a handle.
func (wm *WatchManager) UpdateNotifyMode(handle, mode string) bool {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	key := strings.ToLower(handle)
	for i, e := range wm.entries {
		if strings.ToLower(e.Handle) == key {
			wm.entries[i].NotifyMode = mode
			_ = wm.save()
			return true
		}
	}
	return false
}

// UpdateUserDetails updates user ID and profile info for a handle.
func (wm *WatchManager) UpdateUserDetails(handle, userID string, followers int, profileImage, name string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	key := strings.ToLower(handle)
	for i, e := range wm.entries {
		if strings.ToLower(e.Handle) == key {
			wm.entries[i].UserID = userID
			wm.entries[i].FollowersCount = followers
			wm.entries[i].ProfileImageURL = profileImage
			wm.entries[i].Name = name
			_ = wm.save()
			return
		}
	}
}

// Count returns the number of watched accounts.
func (wm *WatchManager) Count() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return len(wm.entries)
}

// FormatNumber formats an int with comma separators.
func FormatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// FormatCompact renders count compactly.
func FormatCompact(n int) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	case n >= 1_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000), ".0") + "K"
	default:
		return fmt.Sprintf("%d", n)
	}
}

// TrimSince returns how long ago t was in human-readable form.
func TrimSince(t time.Time, loc *time.Location) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
