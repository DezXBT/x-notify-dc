package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// SeenState tracks last-seen notification cursor + per-account tweet IDs.
type SeenState struct {
	CookieHash  string            `json:"cookie_hash"`
	NotifCursor string            `json:"notif_cursor,omitempty"` // most recent notification ID
	LastTweetID map[string]string `json:"last_tweet_id"`          // handle (lower) -> last tweet ID (backup)
	LastReplyID map[string]string `json:"last_reply_id,omitempty"` // handle (lower) -> last reply tweet ID
	UpdatedAt   time.Time         `json:"updated_at"`
	mu          sync.RWMutex
	path        string
}

func NewSeenState(path string) *SeenState {
	s := &SeenState{
		LastTweetID: make(map[string]string),
		LastReplyID: make(map[string]string),
		path:        path,
	}
	s.load()
	return s
}

func (s *SeenState) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, s)
	if s.LastTweetID == nil {
		s.LastTweetID = make(map[string]string)
	}
	if s.LastReplyID == nil {
		s.LastReplyID = make(map[string]string)
	}
}

func (s *SeenState) Save() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		logError("[state] marshal: %v", err)
		return
	}
	if err := os.WriteFile(s.path, data, 0644); err != nil {
		logError("[state] save: %v", err)
	}
}

// ── Notification cursor (primary dedup mechanism) ──

func (s *SeenState) GetNotifCursor() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.NotifCursor
}

func (s *SeenState) SetNotifCursor(cursor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NotifCursor = cursor
}

// ── Per-account tweet ID (backup / compatibility) ──

func (s *SeenState) GetLastTweetID(handle string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastTweetID[handle]
}

func (s *SeenState) SetLastTweetID(handle, tweetID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastTweetID[handle] = tweetID
}

// ── Per-account reply ID (for all+replies mode) ──

func (s *SeenState) GetLastReplyID(handle string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastReplyID[handle]
}

func (s *SeenState) SetLastReplyID(handle, replyID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastReplyID[handle] = replyID
}

// ── Cookie hash ──

func (s *SeenState) SetCookieHash(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CookieHash = hash
}

func (s *SeenState) GetCookieHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CookieHash
}
