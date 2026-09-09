package discovery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProbeReachability_UsesPingResult(t *testing.T) {
	ds := NewDeviceStore("local")
	onlineID := ds.UpsertDevice(&Device{
		Hostnames: []string{"laptop"},
		IPv4:      "192.0.2.10",
		Source:    SourceManual,
	})
	offlineID := ds.UpsertDevice(&Device{
		Hostnames: []string{"printer"},
		IPv4:      "192.0.2.20",
		Source:    SourceManual,
	})
	noIPID := ds.UpsertDevice(&Device{
		Hostnames: []string{"nameless"},
		Source:    SourceManual,
	})

	fake := func(_ context.Context, ip string, _ time.Duration) (time.Duration, error) {
		if ip == "192.0.2.10" {
			return 4 * time.Millisecond, nil
		}
		return 0, errors.New("no reply")
	}

	summary := ds.probeReachability(context.Background(), fake, time.Second, 4)
	if !summary.Available {
		t.Fatal("expected ping available when using a fake pinger")
	}
	if summary.Online != 1 {
		t.Errorf("online = %d, want 1", summary.Online)
	}
	if summary.Probed != 3 {
		t.Errorf("probed = %d, want 3", summary.Probed)
	}

	got := ds.GetDevice(onlineID)
	if !got.Online || got.PingStatus != PingStatusOnline {
		t.Errorf("laptop status = online=%v ping=%q, want online", got.Online, got.PingStatus)
	}
	if got.PingRTTMs != 4 {
		t.Errorf("laptop rtt = %d, want 4", got.PingRTTMs)
	}

	got = ds.GetDevice(offlineID)
	if got.Online || got.PingStatus != PingStatusOffline {
		t.Errorf("printer status = online=%v ping=%q, want offline", got.Online, got.PingStatus)
	}

	got = ds.GetDevice(noIPID)
	if got.Online || got.PingStatus != PingStatusUnknown {
		t.Errorf("no-ip status = online=%v ping=%q, want unknown", got.Online, got.PingStatus)
	}
}

func TestProbeReachability_DoesNotTreatDiscoveryAsOnline(t *testing.T) {
	ds := NewDeviceStore("local")
	id := ds.UpsertDevice(&Device{
		Hostnames: []string{"quiet-tv"},
		IPv4:      "192.0.2.50",
		Source:    SourceMDNS,
	})
	d := ds.GetDevice(id)
	if d.Online {
		t.Fatal("upsert should not mark a device online")
	}
	if d.PingStatus != PingStatusUnknown {
		t.Fatalf("ping status = %q, want unknown", d.PingStatus)
	}
}

func TestTouchDevice_SetsLastDNSQueryWithoutOnline(t *testing.T) {
	ds := NewDeviceStore("local")
	id := ds.UpsertDevice(&Device{
		Hostnames: []string{"phone"},
		IPv4:      "192.0.2.30",
		Source:    SourcePassive,
	})
	ds.TouchDevice(id)
	d := ds.GetDevice(id)
	if d.LastDNSQuery.IsZero() {
		t.Fatal("expected LastDNSQuery to be set")
	}
	if d.Online {
		t.Error("DNS activity should not mark the device online")
	}
}

func TestUpsertPreservesPingAndDNS(t *testing.T) {
	ds := NewDeviceStore("local")
	id := ds.UpsertDevice(&Device{
		Hostnames: []string{"nas"},
		IPv4:      "192.0.2.5",
		Source:    SourceDDNS,
	})
	ds.applyPingResults([]pingResult{{
		id:     id,
		status: PingStatusOnline,
		rtt:    2 * time.Millisecond,
		at:     time.Now(),
	}})
	ds.TouchDevice(id)
	before := ds.GetDevice(id)

	ds.UpsertDevice(&Device{
		ID:        id,
		Hostnames: []string{"nas"},
		IPv4:      "192.0.2.5",
		Source:    SourceMDNS,
		Sources:   []DiscoverySource{SourceMDNS},
	})

	after := ds.GetDevice(id)
	if after.PingStatus != PingStatusOnline || !after.Online {
		t.Errorf("ping status lost on upsert: %+v", after.PingStatus)
	}
	if after.LastDNSQuery.IsZero() {
		t.Error("LastDNSQuery lost on upsert")
	}
	if after.LastDNSQuery.Before(before.LastDNSQuery.Add(-time.Second)) {
		t.Errorf("LastDNSQuery went backwards")
	}
}

func TestHasRecentDNS(t *testing.T) {
	d := &Device{LastDNSQuery: time.Now().Add(-2 * time.Minute)}
	if !d.HasRecentDNS(5 * time.Minute) {
		t.Error("expected recent DNS")
	}
	d.LastDNSQuery = time.Now().Add(-10 * time.Minute)
	if d.HasRecentDNS(5 * time.Minute) {
		t.Error("expected stale DNS")
	}
	d.LastDNSQuery = time.Time{}
	if d.HasRecentDNS(5 * time.Minute) {
		t.Error("zero time should not be recent")
	}
}

func TestPingTarget(t *testing.T) {
	d := &Device{IPv4: "192.0.2.1", IPv6: "fd00::1"}
	if d.PingTarget() != "192.0.2.1" {
		t.Errorf("prefer IPv4, got %q", d.PingTarget())
	}
	d.IPv4 = ""
	if d.PingTarget() != "fd00::1" {
		t.Errorf("fallback IPv6, got %q", d.PingTarget())
	}
}

func TestPingHostInvalidIP(t *testing.T) {
	_, err := pingHost(context.Background(), "not-an-ip", time.Millisecond)
	if err == nil {
		t.Fatal("expected error for invalid IP")
	}
}

func TestPingLocalhost(t *testing.T) {
	if !pingSupported() {
		t.Skip("ICMP/ping not available in this environment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rtt, err := pingHost(ctx, "127.0.0.1", time.Second)
	if err != nil {
		t.Skipf("localhost ping failed (may be blocked): %v", err)
	}
	if rtt <= 0 {
		t.Errorf("expected positive RTT, got %v", rtt)
	}
}
