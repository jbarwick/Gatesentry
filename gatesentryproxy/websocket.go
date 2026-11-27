package gatesentryproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// websocketDial is the TCP dialer used for WebSocket upstreams.
// Tests replace this to assert the dialed address.
var websocketDial = safeDialContext

// tlsClientConfigForWebsocket builds the TLS config for WSS after SSL bump.
// ALPN is http/1.1 only: this dial is about to write an HTTP/1.1 upgrade.
// Tests replace this to trust a fixture CA.
var tlsClientConfigForWebsocket = func(serverName string) *tls.Config {
	return &tls.Config{
		ServerName: serverName,
		NextProtos: []string{"http/1.1"},
	}
}

const websocketHandshakeTimeout = 10 * time.Second

func isWebsocketUpgrade(r *http.Request) bool {
	return r != nil && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// websocketUpstreamAddr is the host:port we dial for a WebSocket upgrade.
// After SSL bump the inner request Host often has no port; use the original
// CONNECT port (connectPort), then 443 for bumped TLS, else 80.
func websocketUpstreamAddr(host string, bumpedTLS bool, connectPort string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	port := connectPort
	if port == "" {
		if bumpedTLS {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

// websocketHostHeader is the Host value sent upstream. A non-default port
// that was taken from the CONNECT target is included. Port 443 (WSS) and
// port 80 (WS) stay off the header when the client omitted them.
func websocketHostHeader(requestHost, dialAddr string, bumpedTLS bool) string {
	requestHost = strings.TrimSpace(requestHost)
	if requestHost == "" {
		return dialAddr
	}
	if _, _, err := net.SplitHostPort(requestHost); err == nil {
		return requestHost
	}
	_, port, err := net.SplitHostPort(dialAddr)
	if err != nil {
		return requestHost
	}
	def := "80"
	if bumpedTLS {
		def = "443"
	}
	if port == def {
		return requestHost
	}
	return net.JoinHostPort(requestHost, port)
}

func websocketServerName(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func hopByHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "host", "connection", "keep-alive", "te", "trailer",
		"transfer-encoding", "proxy-connection", "upgrade":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(key), "proxy-")
	}
}

// connectionHopHeaders are the header names listed in Connection.
// RFC 7230 treats those as hop-by-hop and they must not be forwarded.
func connectionHopHeaders(h http.Header) map[string]bool {
	out := map[string]bool{}
	for _, line := range h.Values("Connection") {
		for _, tok := range strings.Split(line, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			out[strings.ToLower(tok)] = true
		}
	}
	return out
}

func closeWrite(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.CloseWrite()
		return
	}
	if tc, ok := c.(*tls.Conn); ok {
		tc.CloseWrite()
	}
}

// HandleWebsocketConnection upgrades an HTTP request to a bidirectional
// WebSocket tunnel. After SSL bump (h.TLS), the origin speaks TLS on the
// CONNECT port; the relay dials TLS and forwards RFC 6455 headers including
// Connection: Upgrade. Frame payloads are not inspected.
func (h ProxyHandler) HandleWebsocketConnection(r *http.Request, w http.ResponseWriter) {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	addr := websocketUpstreamAddr(host, h.TLS, h.connectPort)
	if addr == "" {
		http.Error(w, "Bad Request: missing host", http.StatusBadRequest)
		return
	}

	rawConn, err := websocketDial(r.Context(), "tcp", addr)
	if err != nil {
		log.Printf("[WebSocket] Failed to connect to upstream %s: %v", addr, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	var serverConn net.Conn = rawConn
	if h.TLS {
		tlsConn := tls.Client(rawConn, tlsClientConfigForWebsocket(websocketServerName(addr)))
		hsCtx, hsCancel := context.WithTimeout(r.Context(), websocketHandshakeTimeout)
		err = tlsConn.HandshakeContext(hsCtx)
		hsCancel()
		if err != nil {
			log.Printf("[WebSocket] TLS handshake with upstream %s: %v", addr, err)
			rawConn.Close()
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
			return
		}
		serverConn = tlsConn
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("[WebSocket] ResponseWriter does not support Hijack")
		serverConn.Close()
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		log.Printf("[WebSocket] Hijack failed: %v", err)
		serverConn.Close()
		return
	}

	reqURI := r.URL.RequestURI()
	if reqURI == "" {
		reqURI = "/"
	}
	fmt.Fprintf(serverConn, "%s %s HTTP/1.1\r\n", r.Method, reqURI)

	upstreamHost := websocketHostHeader(r.Host, addr, h.TLS)
	if upstreamHost == "" {
		upstreamHost = host
	}
	fmt.Fprintf(serverConn, "Host: %s\r\n", upstreamHost)
	fmt.Fprintf(serverConn, "Connection: Upgrade\r\n")
	upgradeVal := r.Header.Get("Upgrade")
	if upgradeVal == "" {
		upgradeVal = "websocket"
	}
	fmt.Fprintf(serverConn, "Upgrade: %s\r\n", upgradeVal)

	extraHop := connectionHopHeaders(r.Header)
	for key, values := range r.Header {
		if hopByHopHeader(key) || extraHop[strings.ToLower(key)] {
			continue
		}
		for _, v := range values {
			fmt.Fprintf(serverConn, "%s: %s\r\n", key, v)
		}
	}
	fmt.Fprintf(serverConn, "\r\n")

	serverBuf := bufio.NewReader(serverConn)
	upstreamResp, err := http.ReadResponse(serverBuf, r)
	if err != nil {
		log.Printf("[WebSocket] Failed to read upstream response: %v", err)
		clientConn.Close()
		serverConn.Close()
		return
	}

	if err := upstreamResp.Write(clientConn); err != nil {
		log.Printf("[WebSocket] Failed to write response to client: %v", err)
		clientConn.Close()
		serverConn.Close()
		return
	}

	if upstreamResp.StatusCode != http.StatusSwitchingProtocols {
		log.Printf("[WebSocket] Upstream returned %d instead of 101", upstreamResp.StatusCode)
		clientConn.Close()
		serverConn.Close()
		return
	}

	if DebugLogging {
		log.Printf("[WebSocket] Tunnel established: %s → %s", r.RemoteAddr, addr)
	}

	var serverReader io.Reader = serverConn
	if serverBuf.Buffered() > 0 {
		serverReader = io.MultiReader(serverBuf, serverConn)
	}

	var clientReader io.Reader = clientConn
	if clientBuf != nil && clientBuf.Reader.Buffered() > 0 {
		clientReader = io.MultiReader(clientBuf, clientConn)
	}

	done := make(chan struct{}, 2)

	go func() {
		io.Copy(clientConn, serverReader)
		closeWrite(clientConn)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(serverConn, clientReader)
		closeWrite(serverConn)
		done <- struct{}{}
	}()

	<-done
	clientConn.SetDeadline(time.Now().Add(5 * time.Second))
	serverConn.SetDeadline(time.Now().Add(5 * time.Second))
	<-done

	clientConn.Close()
	serverConn.Close()

	if DebugLogging {
		log.Printf("[WebSocket] Tunnel closed: %s → %s", r.RemoteAddr, addr)
	}
}
