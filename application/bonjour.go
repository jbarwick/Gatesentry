package gatesentryf

import (
	"log"
	"net"
	"os"
	"strconv"

	"github.com/oleksandr/bonjour"
)

func StartBonjour() {
	log.Println("Starting Bonjour service")

	// Derive admin port from environment (same source of truth as main.go)
	adminPort := 80
	if envPort := os.Getenv("GS_ADMIN_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			adminPort = p
		}
	}

	// Derive base path for TXT record
	basePath := os.Getenv("GS_BASE_PATH")
	if basePath == "" {
		basePath = "/"
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "gatesentry"
	}
	ip := ""
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
				s := ipnet.IP.String()
				if len(s) > 4 && (s[:4] == "192." || s[:3] == "10.") {
					ip = s
					break
				}
				if ip == "" {
					ip = s
				}
			}
		}
	}

	register := func(name, service string, port int) {
		txt := []string{"txtv=1", "app=gatesentry", "path=" + basePath}
		var err error
		if ip != "" {
			_, err = bonjour.RegisterProxy(name, service, "local.", port, host+".local.", ip, txt, nil)
		} else {
			_, err = bonjour.Register(name, service, "", port, txt, nil)
		}
		if err != nil {
			log.Println("[Bonjour] registration error:", name, err.Error())
		}
	}

	go register("GateSentry", "_http._tcp", adminPort)
	go register("GateSentry Proxy", "_gatesentry_proxy._tcp", 10413)

	// Run registration (blocking call)

	// Ctrl+C handling
	// handler := make(chan os.Signal, 1)
	// signal.Notify(handler, os.Interrupt)
	// for sig := range handler {
	//     if sig == os.Interrupt {
	//         s.Shutdown()
	//         time.Sleep(1e9)
	//         break
	//     }
	// }
}
