package gatesentryf

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	gatesentryDnsServer "bitbucket.org/abdullah_irfan/gatesentryf/dns/server"
	gatesentryDomainList "bitbucket.org/abdullah_irfan/gatesentryf/domainlist"
	gatesentryWebserver "bitbucket.org/abdullah_irfan/gatesentryf/webserver"
)

var R *GSRuntime

func Start(webadminport int) *GSRuntime {
	GSVerString := GetApplicationVersion()
	fmt.Println("Starting GateSentry v " + GSVerString)
	R = &GSRuntime{
		WebServerPort:    webadminport,
		FilterFiles:      make(map[string]string),
		DNSServerChannel: make(chan int),
	}
	R.Init()
	LoadFilters()

	// Create the shared domain list manager and run migration from legacy formats.
	// This must happen after Init() (which creates GSSettings) and before both
	// the web server and DNS server start, so they share the same instance.
	R.DomainListManager = gatesentryDomainList.NewDomainListManager(R.GSSettings)
	gatesentryDomainList.MigrateIfNeeded(R.DomainListManager, R.GSSettings)

	// Load all domain lists into the in-memory index (downloads URL-sourced lists).
	// This runs in a goroutine so it doesn't block startup.
	go R.DomainListManager.LoadAllLists()

	fmt.Println("Starting GateSentry webserver on port " + strconv.Itoa(R.WebServerPort))
	go GSwebserverStart(R.WebServerPort)

	return R
}

func Stop() {
	fmt.Println("Stopping GateSentry " + GSVerString)
	gatesentryDnsServer.StopDNSServer()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := gatesentryWebserver.ShutdownAdmin(ctx); err != nil {
		log.Printf("Admin server shutdown: %v", err)
	}
	if R != nil && R.Logger != nil {
		R.Logger.Close()
	}
}
