package gatesentryproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIsWebsocketUpgrade(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"websocket", true},
		{"WebSocket", true},
		{"WEBSOCKET", true},
		{"", false},
		{"h2c", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if tc.val != "" {
			r.Header.Set("Upgrade", tc.val)
		}
		if got := isWebsocketUpgrade(r); got != tc.want {
			t.Errorf("Upgrade %q: got %v want %v", tc.val, got, tc.want)
		}
	}
}

func TestWebsocketUpstreamAddr(t *testing.T) {
	cases := []struct {
		host        string
		tls         bool
		connectPort string
		want        string
	}{
		{"example.com:27019", true, "443", "example.com:27019"},
		{"example.com", true, "27019", "example.com:27019"},
		{"example.com", true, "", "example.com:443"},
		{"example.com", false, "", "example.com:80"},
		{"example.com", false, "8080", "example.com:8080"},
		{"", true, "443", ""},
	}
	for _, tc := range cases {
		got := websocketUpstreamAddr(tc.host, tc.tls, tc.connectPort)
		if got != tc.want {
			t.Errorf("host=%q tls=%v port=%q: got %q want %q", tc.host, tc.tls, tc.connectPort, got, tc.want)
		}
	}
}

func TestWebsocketHostHeaderIncludesNonDefaultPort(t *testing.T) {
	cases := []struct {
		reqHost string
		dial    string
		tls     bool
		want    string
	}{
		{"cm.example.com", "cm.example.com:27019", true, "cm.example.com:27019"},
		{"cm.example.com", "cm.example.com:443", true, "cm.example.com"},
		{"cm.example.com", "cm.example.com:80", false, "cm.example.com"},
		{"cm.example.com:8080", "cm.example.com:8080", false, "cm.example.com:8080"},
	}
	for _, tc := range cases {
		got := websocketHostHeader(tc.reqHost, tc.dial, tc.tls)
		if got != tc.want {
			t.Errorf("req=%q dial=%q tls=%v: got %q want %q", tc.reqHost, tc.dial, tc.tls, got, tc.want)
		}
	}
}

func wsOrigin101(t *testing.T, check func(r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		io.Copy(io.Discard, conn)
		conn.Close()
	}))
}

func doWSClient(t *testing.T, proxyURL, upgrade string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", proxyURL+"/cmsocket/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Upgrade", upgrade)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestWebsocketPlaintextUpgradeForwardsConnectionHeader(t *testing.T) {
	var mu sync.Mutex
	var gotConn, gotUpgrade string
	origin := wsOrigin101(t, func(r *http.Request) {
		mu.Lock()
		gotConn = r.Header.Get("Connection")
		gotUpgrade = r.Header.Get("Upgrade")
		mu.Unlock()
	})
	defer origin.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := ProxyHandler{}
		r.Host = origin.Listener.Addr().String()
		h.HandleWebsocketConnection(r, w)
	}))
	defer proxy.Close()

	resp := doWSClient(t, proxy.URL, "websocket")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.EqualFold(gotConn, "Upgrade") {
		t.Errorf("origin Connection=%q, want Upgrade", gotConn)
	}
	if !strings.EqualFold(gotUpgrade, "websocket") {
		t.Errorf("origin Upgrade=%q, want websocket", gotUpgrade)
	}
}

func TestWebsocketCapitalUpgradeStillTunnels(t *testing.T) {
	origin := wsOrigin101(t, nil)
	defer origin.Close()

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Upgrade", "WebSocket")
	if !isWebsocketUpgrade(r) {
		t.Fatal("Upgrade: WebSocket must match")
	}

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := ProxyHandler{}
		req.Host = origin.Listener.Addr().String()
		h.HandleWebsocketConnection(req, w)
	}))
	defer proxy.Close()

	resp := doWSClient(t, proxy.URL, "WebSocket")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestWebsocketBumpedUsesConnectPortNot80(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	originAddr := ln.Addr().String()
	_, originPort, err := net.SplitHostPort(originAddr)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		_, _ = http.ReadRequest(br)
		fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	}()

	var dialed string
	origDial := websocketDial
	websocketDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return net.Dial("tcp", originAddr)
	}
	defer func() { websocketDial = origDial }()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := ProxyHandler{TLS: false, connectPort: originPort}
		r.Host = "cm.example.com"
		h.HandleWebsocketConnection(r, w)
	}))
	defer proxy.Close()

	resp := doWSClient(t, proxy.URL, "websocket")
	defer resp.Body.Close()
	want := net.JoinHostPort("cm.example.com", originPort)
	if dialed != want {
		t.Fatalf("dialed %q, want %q", dialed, want)
	}
	if strings.HasSuffix(dialed, ":80") && originPort != "80" {
		t.Fatalf("dialed port 80: %q", dialed)
	}
}

func TestWebsocketWSSAfterBumpDialsTLSOnConnectPort(t *testing.T) {
	var mu sync.Mutex
	var gotHost, gotConn string
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHost = r.Host
		gotConn = r.Header.Get("Connection")
		mu.Unlock()
		if !strings.EqualFold(r.Header.Get("Connection"), "Upgrade") {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", 500)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		io.Copy(io.Discard, conn)
		conn.Close()
	}))
	defer origin.Close()

	_, originPort, err := net.SplitHostPort(origin.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if originPort == "443" || originPort == "80" {
		t.Fatal("origin port collided with a scheme default")
	}

	var dialed, serverName string
	var protos []string
	origTLS := tlsClientConfigForWebsocket
	tlsClientConfigForWebsocket = func(name string) *tls.Config {
		serverName = name
		cfg := origTLS(name)
		cfg.InsecureSkipVerify = true
		protos = append([]string(nil), cfg.NextProtos...)
		return cfg
	}
	defer func() { tlsClientConfigForWebsocket = origTLS }()

	origDial := websocketDial
	websocketDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return net.Dial(network, origin.Listener.Addr().String())
	}
	defer func() { websocketDial = origDial }()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := ProxyHandler{TLS: true, connectPort: originPort}
		r.Host = "cm.example.com"
		h.HandleWebsocketConnection(r, w)
	}))
	defer proxy.Close()

	resp := doWSClient(t, proxy.URL, "websocket")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("WSS after bump status %d", resp.StatusCode)
	}
	wantDial := net.JoinHostPort("cm.example.com", originPort)
	if dialed != wantDial {
		t.Fatalf("dialed %q, want %q", dialed, wantDial)
	}
	if serverName != "cm.example.com" {
		t.Fatalf("SNI %q", serverName)
	}
	if len(protos) != 1 || protos[0] != "http/1.1" {
		t.Fatalf("upstream ALPN %v, want [http/1.1]", protos)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotHost != wantDial {
		t.Fatalf("origin Host %q, want %q", gotHost, wantDial)
	}
	if !strings.EqualFold(gotConn, "Upgrade") {
		t.Fatalf("origin Connection %q", gotConn)
	}
}

func TestWebsocketOrigin400PassedThrough(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
	}))
	defer origin.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := ProxyHandler{}
		r.Host = origin.Listener.Addr().String()
		h.HandleWebsocketConnection(r, w)
	}))
	defer proxy.Close()

	resp := doWSClient(t, proxy.URL, "websocket")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 from origin", resp.StatusCode)
	}
}
