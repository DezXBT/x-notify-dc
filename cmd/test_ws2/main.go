package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"nhooyr.io/websocket"
)

func main() {
	authToken := os.Getenv("AUTH_TOKEN")
	ct0 := os.Getenv("CT0")
	if authToken == "" || ct0 == "" {
		fmt.Println("Usage: AUTH_TOKEN=*** CT0=yyy go run cmd/test_ws2/main.go")
		os.Exit(1)
	}

	// Create uTLS-based HTTP/2 transport
	// nhooyr.io/websocket uses this for the WebSocket handshake over HTTP/2
	h2Transport := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			host := addr
			if h, _, err := net.SplitHostPort(addr); err == nil {
				host = h
			}
			config := &utls.Config{ServerName: host}

			dialer := &net.Dialer{Timeout: 15 * time.Second}
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			uconn := utls.UClient(conn, config, utls.HelloChrome_131)
			if err := uconn.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}

			state := uconn.ConnectionState()
			fmt.Printf("[utls] ALPN: %s\n", state.NegotiatedProtocol)

			return uconn, nil
		},
		AllowHTTP: true,
	}

	client := &http.Client{Transport: h2Transport}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	endpoint := "wss://x.com/i/api/1.1/live_pipeline/update_subscriptions"

	header := http.Header{}
	header.Set("authorization", "Bearer AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs=1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA")
	header.Set("x-twitter-auth-type", "OAuth2Session")
	header.Set("x-twitter-active-user", "yes")
	header.Set("x-csrf-token", ct0)
	header.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", authToken, ct0))
	header.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	header.Set("origin", "https://x.com")

	opts := &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: header,
	}

	fmt.Printf("[ws] dialing %s...\n", endpoint)
	conn, resp, err := websocket.Dial(ctx, endpoint, opts)
	if err != nil {
		fmt.Printf("[ws] ❌ dial failed: %v\n", err)
		if resp != nil {
			fmt.Printf("[ws] HTTP status: %d, proto: %s\n", resp.StatusCode, resp.Proto)
		}
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "test")

	fmt.Printf("[ws] ✅ connected! Proto: %s, Status: %d\n", resp.Proto, resp.StatusCode)

	// Subscribe
	subMsg := `{"subscribe":[{"topic":"notification:1878756955436793857","sub_topic":""}],"unsubscribe":[]}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(subMsg)); err != nil {
		fmt.Printf("[ws] ❌ write failed: %v\n", err)
		return
	}
	fmt.Printf("[ws] ✅ subscribed, reading messages for 30s...\n")

	for {
		select {
		case <-ctx.Done():
			fmt.Println("[ws] timeout, exiting")
			return
		default:
		}

		msgType, data, err := conn.Read(ctx)
		if err != nil {
			fmt.Printf("[ws] read error: %v\n", err)
			return
		}
		fmt.Printf("[ws] 📨 [%s] %s\n", msgType, truncate(string(data), 300))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
