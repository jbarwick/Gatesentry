package discovery

import (
	"context"
	"sync"
	"time"
)

// ProbeSummary is returned after a reachability sweep.
type ProbeSummary struct {
	Available bool `json:"ping_available"`
	Probed    int  `json:"probed"`
	Online    int  `json:"online"`
}

type pingResult struct {
	id     string
	status string
	rtt    time.Duration
	at     time.Time
}

// ProbeReachability pings every device with an IP address and updates
// Online / PingStatus. Devices without an IP are marked unknown.
// If ICMP (and the ping binary) are unavailable, every device is unknown
// rather than offline.
func (ds *DeviceStore) ProbeReachability(ctx context.Context) ProbeSummary {
	if ctx == nil {
		ctx = context.Background()
	}
	if !pingSupported() {
		ds.setAllPingStatus(PingStatusUnknown)
		return ProbeSummary{Available: false}
	}
	return ds.probeReachability(ctx, pingHost, defaultPingTimeout, maxConcurrentPings)
}

// ProbeDevice pings a single device and returns the updated copy.
func (ds *DeviceStore) ProbeDevice(ctx context.Context, id string) *Device {
	if ctx == nil {
		ctx = context.Background()
	}
	d := ds.GetDevice(id)
	if d == nil {
		return nil
	}

	now := time.Now()
	if !pingSupported() {
		ds.applyPingResults([]pingResult{{id: id, status: PingStatusUnknown, at: now}})
		return ds.GetDevice(id)
	}

	ip := d.PingTarget()
	if ip == "" {
		ds.applyPingResults([]pingResult{{id: id, status: PingStatusUnknown, at: now}})
		return ds.GetDevice(id)
	}

	status := PingStatusOffline
	var rtt time.Duration
	if measured, err := pingHost(ctx, ip, defaultPingTimeout); err == nil {
		status = PingStatusOnline
		rtt = measured
	}
	ds.applyPingResults([]pingResult{{id: id, status: status, rtt: rtt, at: time.Now()}})
	return ds.GetDevice(id)
}

func (ds *DeviceStore) probeReachability(ctx context.Context, pinger pingFunc, timeout time.Duration, concurrency int) ProbeSummary {
	devices := ds.GetAllDevices()
	if concurrency < 1 {
		concurrency = 1
	}

	results := make([]pingResult, 0, len(devices))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	appendResult := func(r pingResult) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}

	for i := range devices {
		d := devices[i]
		ip := d.PingTarget()
		if ip == "" {
			appendResult(pingResult{
				id:     d.ID,
				status: PingStatusUnknown,
				at:     time.Now(),
			})
			continue
		}

		wg.Add(1)
		go func(id, ip string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				appendResult(pingResult{id: id, status: PingStatusUnknown, at: time.Now()})
				return
			}
			defer func() { <-sem }()

			status := PingStatusOffline
			var rtt time.Duration
			if measured, err := pinger(ctx, ip, timeout); err == nil {
				status = PingStatusOnline
				rtt = measured
			}
			appendResult(pingResult{id: id, status: status, rtt: rtt, at: time.Now()})
		}(d.ID, ip)
	}

	wg.Wait()
	ds.applyPingResults(results)

	summary := ProbeSummary{Available: true, Probed: len(results)}
	for _, r := range results {
		if r.status == PingStatusOnline {
			summary.Online++
		}
	}
	return summary
}

func (ds *DeviceStore) setAllPingStatus(status string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	now := time.Now()
	for _, d := range ds.devices {
		d.PingStatus = status
		d.Online = status == PingStatusOnline
		d.LastPing = now
		if status != PingStatusOnline {
			d.PingRTTMs = 0
		}
	}
}

func (ds *DeviceStore) applyPingResults(results []pingResult) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for _, r := range results {
		d := ds.devices[r.id]
		if d == nil {
			continue
		}
		d.PingStatus = r.status
		d.Online = r.status == PingStatusOnline
		d.LastPing = r.at
		if r.status == PingStatusOnline {
			d.PingRTTMs = r.rtt.Milliseconds()
			if d.PingRTTMs == 0 && r.rtt > 0 {
				d.PingRTTMs = 1
			}
		} else {
			d.PingRTTMs = 0
		}
	}
}
