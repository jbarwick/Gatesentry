package gatesentryWebserver

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	gatesentry2storage "bitbucket.org/abdullah_irfan/gatesentryf/storage"
	gatesentryWebserverEndpoints "bitbucket.org/abdullah_irfan/gatesentryf/webserver/endpoints"
)

var (
	adminHTTPSMu     sync.Mutex
	adminHTTPSServer *http.Server
)

// AdminHTTPSPort is GS_ADMIN_PORT_SSL, or 9877 if unset.
func AdminHTTPSPort() string {
	p := strings.TrimSpace(os.Getenv("GS_ADMIN_PORT_SSL"))
	if p == "" {
		return "9877"
	}
	return p
}

func applyAdminHTTPS(handler http.Handler, settings *gatesentry2storage.MapStore) {
	port := AdminHTTPSPort()
	enabled := settings != nil && settings.Get("enable_admin_https") == "true"
	certPEM := ""
	keyPEM := ""
	if settings != nil {
		certPEM = settings.Get("admin_https_certpem")
		keyPEM = settings.Get("admin_https_keypem")
	}

	stopAdminHTTPS()

	if !enabled {
		log.Printf("[Admin HTTPS] disabled (port %s unused)", port)
		return
	}
	if err := gatesentryWebserverEndpoints.ValidateServerTLS(certPEM, keyPEM); err != nil {
		log.Printf("[Admin HTTPS] not listening: %v", err)
		return
	}

	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		log.Printf("[Admin HTTPS] not listening: %v", err)
		return
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{pair},
		},
	}

	adminHTTPSMu.Lock()
	adminHTTPSServer = srv
	adminHTTPSMu.Unlock()

	go func() {
		log.Printf("[Admin HTTPS] listening on :%s", port)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("[Admin HTTPS] server error: %v", err)
		}
	}()
}

func stopAdminHTTPS() {
	adminHTTPSMu.Lock()
	srv := adminHTTPSServer
	adminHTTPSServer = nil
	adminHTTPSMu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("[Admin HTTPS] shutdown: %v", err)
	}
}

func shutdownAdminHTTPS(ctx context.Context) error {
	adminHTTPSMu.Lock()
	srv := adminHTTPSServer
	adminHTTPSServer = nil
	adminHTTPSMu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
