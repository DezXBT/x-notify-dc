package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// SeenState tracks last-seen tweet IDs per account and cookie hash for re-sync detection.
type SeenState struct {
	CookieHash  string            `json:"cookie_hash"`
	LastTweetID map[string]string `json:"last_tweet_id"` // handle (lower) -> last tweet ID
	UpdatedAt   time.Time         `json:"updated_at"`
	mu          sync.RWMutex
	path        string
}

func NewSeenState(path string) *SeenState {
	s := &SeenState{
		LastTweetID: make(map[string]string),
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
