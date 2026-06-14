package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// newUTLSClient returns an *http.Client whose TLS fingerprint matches Chrome 133,
// bypassing Cloudflare bot detection on x.com. Handles both HTTP/1.1 and HTTP/2.
func newUTLSClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: newUTLSRoundTripper(),
	}
}

// utlsRoundTripper routes requests to HTTP/1.1 or HTTP/2 transport based on ALPN.
type utlsRoundTripper struct {
	hello utls.ClientHelloID

	mu    sync.Mutex
	proto map[string]string // host:port -> negotiated ALPN

	h1 *http.Transport
	h2 *http2.Transport
}

func newUTLSRoundTripper() *utlsRoundTripper {
	rt := &utlsRoundTripper{
		hello: utls.HelloChrome_133,
		proto: map[string]string{},
	}
	rt.h1 = &http.Transport{
		MaxIdleConns:        20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return rt.dialUTLS(ctx, addr, []string{"http/1.1"})
		},
	}
	rt.h2 = &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return rt.dialUTLS(ctx, addr, []string{"h2", "http/1.1"})
		},
	}
	return rt
}

func (rt *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	addr := canonicalAddr(req.URL)
	proto, err := rt.protocolFor(req.Context(), addr)
	if err != nil {
		return nil, err
	}
	if proto == "h2" {
		return rt.h2.RoundTrip(req)
	}
	return rt.h1.RoundTrip(req)
}

// protocolFor probes the ALPN protocol for a host once and caches it.
func (rt *utlsRoundTripper) protocolFor(ctx context.Context, addr string) (string, error) {
	rt.mu.Lock()
	if p, ok := rt.proto[addr]; ok {
		rt.mu.Unlock()
		return p, nil
	}
	rt.mu.Unlock()

	conn, err := rt.dialUTLS(ctx, addr, []string{"h2", "http/1.1"})
	if err != nil {
		return "", err
	}
	p := conn.(*utls.UConn).ConnectionState().NegotiatedProtocol
	_ = conn.Close()
	if p == "" {
		p = "http/1.1"
	}
	rt.mu.Lock()
	rt.proto[addr] = p
	rt.mu.Unlock()
	return p, nil
}

func (rt *utlsRoundTripper) dialUTLS(ctx context.Context, addr string, alpn []string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	raw, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	spec, err := utls.UTLSIdToSpec(rt.hello)
	if err != nil {
		raw.Close()
		return nil, err
	}
	for _, ext := range spec.Extensions {
		if a, ok := ext.(*utls.ALPNExtension); ok {
			a.AlpnProtocols = alpn
		}
	}
	uconn := utls.UClient(raw, &utls.Config{ServerName: host}, utls.HelloCustom)
	if err := uconn.ApplyPreset(&spec); err != nil {
		raw.Close()
		return nil, err
	}
	if err := uconn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return uconn, nil
}

func canonicalAddr(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	return net.JoinHostPort(u.Hostname(), "443")
}
