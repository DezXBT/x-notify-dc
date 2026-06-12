package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DezXBT/x-notify-dc/pkg/transaction"
)

// wsNotifier connects to X's live_pipeline WebSocket for real-time notifications.
// When a push event arrives, it calls OnPush to trigger a normal REST poll.
type wsNotifier struct {
	cookies    []CookiePair
	txProvider *transaction.Provider

	mu      sync.Mutex
	conn    *websocket.Conn
	stopCh  chan struct{}
	running bool
	OnPush  func() // called when WS receives a notification push
}

func newWSNotifier(cookies []CookiePair) *wsNotifier {
	return &wsNotifier{
		cookies:    cookies,
		txProvider: transaction.NewProvider(),
	}
}

// IsAvailable returns true if live_pipeline WS can be used.
func (w *wsNotifier) IsAvailable() bool {
	return len(w.cookies) > 0 && w.txProvider != nil
}

// Start connects to the WebSocket and begins listening.
// Returns error if connection fails (caller should fall back to polling).
func (w *wsNotifier) Start(ctx context.Context, onPush func()) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return fmt.Errorf("already running")
	}

	w.OnPush = onPush
	w.stopCh = make(chan struct{})

	if err := w.connect(ctx); err != nil {
		return fmt.Errorf("ws connect: %w", err)
	}

	w.running = true
	go w.readLoop(ctx)
	go w.keepalive(ctx)

	logInfo("[ws] ✅ live_pipeline connected — real-time mode")
	return nil
}

// Stop closes the WebSocket connection.
func (w *wsNotifier) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.running {
		return
	}
	w.running = false
	close(w.stopCh)
	if w.conn != nil {
		w.conn.Close()
	}
	logInfo("[ws] live_pipeline stopped")
}

func (w *wsNotifier) connect(ctx context.Context) error {
	endpoint := "wss://x.com/i/api/1.1/live_pipeline/update_subscriptions"

	// Generate x-client-transaction-id
	txID, err := w.txProvider.TransactionID("GET", "/i/api/1.1/live_pipeline/update_subscriptions")
	if err != nil {
		logWarn("[ws] failed to generate transaction ID: %v", err)
	}

	// Build headers — use first cookie
	c := w.cookies[0]
	header := http.Header{}
	header.Set("authorization", "Bearer "+bearerToken)
	header.Set("x-twitter-auth-type", "OAuth2Session")
	header.Set("x-twitter-active-user", "yes")
	header.Set("x-csrf-token", c.Ct0)
	header.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", c.AuthToken, c.Ct0))
	header.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	header.Set("origin", "https://x.com")
	header.Set("accept-language", "en-US,en;q=0.9")
	if txID != "" {
		header.Set("x-client-transaction-id", txID)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}

	conn, resp, err := dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial %s: HTTP %d", endpoint, resp.StatusCode)
		}
		return fmt.Errorf("dial %s: %w", endpoint, err)
	}

	w.conn = conn

	// Subscribe to notifications for all cookie accounts
	if err := w.subscribe(); err != nil {
		conn.Close()
		return fmt.Errorf("subscribe: %w", err)
	}

	return nil
}

// subscribe sends the subscription message for notification topics.
func (w *wsNotifier) subscribe() error {
	topics := []map[string]string{}
	for _, c := range w.cookies {
		userID, err := w.getUserID(c)
		if err != nil {
			logWarn("[ws] failed to get user ID for %s: %v", c.Label, err)
			continue
		}
		topics = append(topics, map[string]string{
			"topic":     "notification:" + userID,
			"sub_topic": "",
		})
	}

	if len(topics) == 0 {
		return fmt.Errorf("no valid user IDs for subscription")
	}

	subMsg := map[string]any{
		"subscribe":   topics,
		"unsubscribe": []string{},
	}

	msg, _ := json.Marshal(subMsg)
	logInfo("[ws] subscribing to %d notification topics", len(topics))
	return w.conn.WriteMessage(websocket.TextMessage, msg)
}

// getUserID calls verify_credentials to get the user ID for a cookie pair.
func (w *wsNotifier) getUserID(c CookiePair) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", "https://api.x.com/1.1/account/verify_credentials.json", nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("authorization", "Bearer "+bearerToken)
	req.Header.Set("x-twitter-auth-type", "OAuth2Session")
	req.Header.Set("x-twitter-active-user", "yes")
	req.Header.Set("x-csrf-token", c.Ct0)
	req.Header.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", c.AuthToken, c.Ct0))
	req.Header.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	txID, err := w.txProvider.TransactionID("GET", "/1.1/account/verify_credentials.json")
	if err == nil && txID != "" {
		req.Header.Set("x-client-transaction-id", txID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("verify_credentials: HTTP %d", resp.StatusCode)
	}

	var result struct {
		IDStr string `json:"id_str"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.IDStr == "" {
		return "", fmt.Errorf("empty user ID")
	}

	logInfo("[ws] user ID: %s (label: %s)", result.IDStr, c.Label)
	return result.IDStr, nil
}

// readLoop reads messages from the WebSocket and triggers poll on notifications.
func (w *wsNotifier) readLoop(ctx context.Context) {
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		_, message, err := w.conn.ReadMessage()
		if err != nil {
			if w.isStopped() {
				return
			}
			logWarn("[ws] read error: %v", err)
			w.reconnect(ctx)
			return
		}

		w.handleMessage(message)
	}
}

// handleMessage parses incoming WebSocket messages.
// On notification push → trigger REST poll via OnPush callback.
func (w *wsNotifier) handleMessage(data []byte) {
	msg := string(data)

	// Keepalive
	if strings.Contains(msg, `"keep_alive"`) {
		return
	}

	// Try to parse as live pipeline message
	var pipelineMsg struct {
		Topic   string `json:"topic"`
		Payload any    `json:"payload"`
	}

	if err := json.Unmarshal(data, &pipelineMsg); err != nil {
		logDebug("[ws] unparsed message: %s", truncateStr(msg, 200))
		return
	}

	// Log the topic
	if pipelineMsg.Topic != "" {
		logInfo("[ws] 🔔 push received: %s", pipelineMsg.Topic)

		// Any notification topic → trigger REST poll
		if strings.HasPrefix(pipelineMsg.Topic, "notification:") {
			if w.OnPush != nil {
				w.OnPush()
			}
		}
	}
}

// keepalive sends periodic pings to keep the connection alive.
func (w *wsNotifier) keepalive(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.conn != nil {
				if err := w.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					logWarn("[ws] ping failed: %v", err)
					return
				}
			}
		}
	}
}

// reconnect attempts to re-establish the WebSocket connection with backoff.
func (w *wsNotifier) reconnect(ctx context.Context) {
	w.mu.Lock()
	if w.conn != nil {
		w.conn.Close()
		w.conn = nil
	}
	w.mu.Unlock()

	backoff := 2 * time.Second
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		logInfo("[ws] attempting reconnect (backoff %s)", backoff)

		if err := w.connect(ctx); err != nil {
			logWarn("[ws] reconnect failed: %v", err)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		logInfo("[ws] ✅ reconnected")
		go w.readLoop(ctx)
		return
	}
}

func (w *wsNotifier) isStopped() bool {
	select {
	case <-w.stopCh:
		return true
	default:
		return false
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
