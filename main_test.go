package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	wa, wb := newWire(a), newWire(b)
	defer wa.close()
	defer wb.close()
	want := []byte("test-frame-payload")
	done := make(chan error, 1)
	go func() { done <- wa.send(msgData, 0x12345678, want) }()
	got, err := wb.receive()
	if err != nil {
		t.Fatal(err)
	}
	if got.typ != msgData || got.id != 0x12345678 || string(got.data) != string(want) {
		t.Fatalf("frame mismatch: %#v", got)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReverseStreamIntegration(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	a, b := net.Pipe()
	hs := newHubSession(a)
	as := newAgentSession(b, map[string]string{"main": echo.Addr().String()})
	go hs.run()
	go as.run()
	defer hs.shutdown()
	defer as.shutdown()
	clientTunnel, client := net.Pipe()
	defer client.Close()
	if _, _, err = hs.addStream(clientTunnel, "main"); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("reverse-tunnel-test")
	if _, err = client.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo mismatch: got %q want %q", got, payload)
	}
}

func TestFingerprintParser(t *testing.T) {
	if _, err := normalizeFingerprint("sha256:" + "aa"); err == nil {
		t.Fatal("expected error for short fingerprint")
	}
	if b, err := normalizeFingerprint("aa:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil || len(b) != 32 {
		t.Fatalf("valid fingerprint rejected: %v", err)
	}
}

func TestMultipleMappingIDsRouteToCorrectTargets(t *testing.T) {
	targetOne, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetOne.Close()
	targetTwo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetTwo.Close()
	startEcho := func(ln net.Listener, response string) {
		go func() {
			for {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					b := make([]byte, 64)
					n, _ := c.Read(b)
					if n > 0 {
						_, _ = c.Write([]byte(response))
					}
				}(c)
			}
		}()
	}
	startEcho(targetOne, "target-one")
	startEcho(targetTwo, "target-two")
	a, b := net.Pipe()
	hs := newHubSession(a)
	as := newAgentSession(b, map[string]string{"one": targetOne.Addr().String(), "two": targetTwo.Addr().String()})
	go hs.run()
	go as.run()
	defer hs.shutdown()
	defer as.shutdown()
	for _, test := range []struct{ mapping, want string }{{"one", "target-one"}, {"two", "target-two"}} {
		tunnel, client := net.Pipe()
		_, _, e := hs.addStream(tunnel, test.mapping)
		if e != nil {
			client.Close()
			t.Fatal(e)
		}
		_ = client.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = client.Write([]byte("hello"))
		got := make([]byte, len(test.want))
		if _, e := io.ReadFull(client, got); e != nil {
			client.Close()
			t.Fatal(e)
		}
		if string(got) != test.want {
			client.Close()
			t.Fatalf("mapping %s: got %q want %q", test.mapping, got, test.want)
		}
		client.Close()
	}
}

func TestLegacyConfigNormalization(t *testing.T) {
	hub := normalizeConfig(Config{Role: "hub", PublicListen: "0.0.0.0:443"})
	if len(hub.Ports) != 1 || hub.Ports[0].ID != "main" || hub.Ports[0].Listen != "0.0.0.0:443" {
		t.Fatalf("legacy hub config not normalized: %#v", hub.Ports)
	}
	agent := normalizeConfig(Config{Role: "agent", Target: "127.0.0.1:443"})
	if len(agent.Ports) != 1 || agent.Ports[0].ID != "main" || agent.Ports[0].Target != "127.0.0.1:443" {
		t.Fatalf("legacy agent config not normalized: %#v", agent.Ports)
	}
}

func TestRelayTCPPassThrough(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(c)
		}
	}()
	relayClient, client := net.Pipe()
	defer client.Close()
	go relayTCP(relayClient, echo.Addr().String())
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("relay-pass-through")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q want %q", got, payload)
	}
}

func TestAuthProtocolRequiresCurrentVersion(t *testing.T) {
	token := strings.Repeat("a", 64)
	if got := authProtocol("RREV2 "+token+"\n", token); got != "RREV2" {
		t.Fatalf("current protocol rejected: %q", got)
	}
	if validAuth("RREV1 "+token+"\n", token) {
		t.Fatal("obsolete protocol accepted")
	}
	if validAuth("RREV2 "+strings.Repeat("b", 64), token) {
		t.Fatal("invalid token accepted")
	}
}

func TestJitteredBackoffWithinExpectedBounds(t *testing.T) {
	base := 10 * time.Second
	min, max := base*4/5, base*6/5
	for i := 0; i < 100; i++ {
		got := jitteredBackoff(base)
		if got < min || got > max {
			t.Fatalf("jittered backoff %s outside expected range [%s, %s]", got, min, max)
		}
	}
	if got := jitteredBackoff(0); got != 0 {
		t.Fatalf("zero backoff should remain zero, got %s", got)
	}
}

func TestValidateTCPAddressRequiresExplicitHost(t *testing.T) {
	if err := validateTCPAddress("test", ":443"); err == nil {
		t.Fatal("empty host should be rejected")
	}
	if err := validateTCPAddress("test", "127.0.0.1:443"); err != nil {
		t.Fatalf("valid TCP address rejected: %v", err)
	}
}

func TestGeoDisplayUnknown(t *testing.T) {
	ip, country := geoDisplay(ServerGeo{})
	if ip != "Unavailable" || country != "Unknown" {
		t.Fatalf("unexpected unknown identity: ip=%q country=%q", ip, country)
	}
}

func TestGeoDisplayKnown(t *testing.T) {
	g := ServerGeo{IP: "203.0.113.9", Country: "Exampleland", CountryCode: "EX", UpdatedAt: time.Now(), Valid: true}
	ip, country := geoDisplay(g)
	if ip != "203.0.113.9" || country != "Exampleland (EX)" {
		t.Fatalf("unexpected identity: ip=%q country=%q", ip, country)
	}
}

func TestGeoDisplayRejectsIPv6(t *testing.T) {
	g := ServerGeo{IP: "2a01:4f8:c010:33c3::1", Country: "Germany", CountryCode: "DE", UpdatedAt: time.Now(), Valid: true}
	ip, country := geoDisplay(g)
	if ip != "Unavailable" || country != "Unknown" {
		t.Fatalf("IPv6 must not be shown as the primary server IPv4: ip=%q country=%q", ip, country)
	}
}

func TestNormalizeServerGeoInvalidatesCachedIPv6(t *testing.T) {
	oldAttempt := time.Now().Add(-time.Minute)
	g := ServerGeo{IP: "2a01:4f8:c010:33c3::1", Country: "Germany", CountryCode: "DE", UpdatedAt: oldAttempt, LastAttempt: oldAttempt, Valid: true}
	got := normalizeServerGeo(g)
	if got.Valid || got.IP != "" || got.Country != "" || got.CountryCode != "" || !got.LastAttempt.IsZero() {
		t.Fatalf("IPv6 cache should be invalidated and force a new IPv4 lookup: %#v", got)
	}
}

func TestNormalizeServerGeoKeepsIPv4(t *testing.T) {
	g := ServerGeo{IP: "203.0.113.9", Country: "Exampleland", CountryCode: "EX", UpdatedAt: time.Now(), Valid: true}
	got := normalizeServerGeo(g)
	if !got.Valid || got.IP != "203.0.113.9" || got.Country != "Exampleland" {
		t.Fatalf("valid IPv4 cache changed unexpectedly: %#v", got)
	}
}

func TestAgentTunnelChannelDefaultsAndBounds(t *testing.T) {
	legacy := normalizeConfig(Config{Role: "agent", Target: "127.0.0.1:443"})
	if legacy.TunnelChannels != defaultTunnelChannels {
		t.Fatalf("legacy config should default to %d independent lanes, got %d", defaultTunnelChannels, legacy.TunnelChannels)
	}
	invalid := Config{
		Version: version, Role: "agent", Token: strings.Repeat("a", 64),
		HubAddr: "127.0.0.1:9443", Fingerprint: strings.Repeat("a", 64),
		TunnelChannels: maxTunnelChannels + 1,
		Ports:          []PortMapping{{ID: "main", Target: "127.0.0.1:443"}},
	}
	if err := validateConfig(invalid); err == nil || !strings.Contains(err.Error(), "tunnel_channels") {
		t.Fatalf("expected tunnel channel limit error, got %v", err)
	}
}

func TestHubSessionPoolBalancesAndKeepsHealthyLanes(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(c)
		}
	}()

	hubWire1, agentWire1 := net.Pipe()
	hubWire2, agentWire2 := net.Pipe()
	hub1 := newHubSession(hubWire1)
	hub2 := newHubSession(hubWire2)
	agent1 := newAgentSession(agentWire1, map[string]string{"main": echo.Addr().String()})
	agent2 := newAgentSession(agentWire2, map[string]string{"main": echo.Addr().String()})
	h := &hubServer{}
	h.registerSession(hub1)
	h.registerSession(hub2)
	go hub1.run()
	go hub2.run()
	go agent1.run()
	go agent2.run()
	defer func() {
		h.unregisterSession(hub1)
		h.unregisterSession(hub2)
		hub1.shutdown()
		hub2.shutdown()
		agent1.shutdown()
		agent2.shutdown()
	}()

	clients := make([]net.Conn, 0, 30)
	defer func() {
		for _, c := range clients {
			_ = c.Close()
		}
	}()
	for i := 0; i < 30; i++ {
		tunnelSide, clientSide := net.Pipe()
		selected := h.current()
		if selected == nil {
			_ = tunnelSide.Close()
			_ = clientSide.Close()
			t.Fatal("expected a healthy lane from the pool")
		}
		if _, _, err := selected.addStream(tunnelSide, "main"); err != nil {
			_ = clientSide.Close()
			t.Fatalf("open stream %d: %v", i, err)
		}
		clients = append(clients, clientSide)
	}
	if got1, got2 := hub1.activeStreams(), hub2.activeStreams(); got1 == 0 || got2 == 0 {
		t.Fatalf("new streams were not distributed across both lanes: lane1=%d lane2=%d", got1, got2)
	}

	var traffic sync.WaitGroup
	errCh := make(chan error, len(clients))
	for i, c := range clients {
		traffic.Add(1)
		go func(i int, c net.Conn) {
			defer traffic.Done()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			payload := []byte(fmt.Sprintf("parallel-lane-test-%02d", i))
			if _, err := c.Write(payload); err != nil {
				errCh <- fmt.Errorf("write client %d: %w", i, err)
				return
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(c, got); err != nil {
				errCh <- fmt.Errorf("read client %d: %w", i, err)
				return
			}
			if string(got) != string(payload) {
				errCh <- fmt.Errorf("client %d echo mismatch: got %q, want %q", i, got, payload)
			}
		}(i, c)
	}
	traffic.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// Losing one lane must not mark the Hub offline while another remains.
	h.unregisterSession(hub1)
	if !metricConnected.Load() {
		t.Fatal("Hub should remain connected while another lane is alive")
	}
	if h.current() != hub2 {
		t.Fatal("Hub did not keep routing through the remaining healthy lane")
	}
}

func TestMultipleAuthenticatedTLSTunnelLanesCarryThirtyClients(t *testing.T) {
	certPath := t.TempDir() + "/hub.crt"
	keyPath := t.TempDir() + "/hub.key"
	fingerprint, err := generateCertificate(certPath, keyPath)
	if err != nil {
		t.Fatalf("generate test certificate: %v", err)
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load test certificate: %v", err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}

	control, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	h := &hubServer{cfg: Config{Token: token}, sessions: make(map[*hubSession]struct{})}
	go func() {
		for {
			conn, e := control.Accept()
			if e != nil {
				return
			}
			go h.serveAgent(conn, tlsCfg)
		}
	}()

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = control.Close()
		t.Fatal(err)
	}
	go func() {
		for {
			conn, e := echo.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }(conn)
		}
	}()

	pin, err := normalizeFingerprint(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Version: version, Role: "agent", HubAddr: control.Addr().String(), Token: token,
		Fingerprint: fingerprint, TunnelChannels: 2,
		Ports: []PortMapping{{ID: "main", Target: echo.Addr().String()}},
	}
	agentDone := make(chan error, cfg.TunnelChannels)
	for channel := 1; channel <= cfg.TunnelChannels; channel++ {
		go func(channel int) { agentDone <- connectAndRunAgent(cfg, pin, channel) }(channel)
	}

	clients := make([]net.Conn, 0, 30)
	var publicListener net.Listener
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
		if publicListener != nil {
			_ = publicListener.Close()
		}
		_ = echo.Close()
		_ = control.Close()
		h.mu.RLock()
		lanes := make([]*hubSession, 0, len(h.sessions))
		for lane := range h.sessions {
			lanes = append(lanes, lane)
		}
		h.mu.RUnlock()
		for _, lane := range lanes {
			lane.shutdown()
		}
		for i := 0; i < cfg.TunnelChannels; i++ {
			select {
			case <-agentDone:
			case <-time.After(2 * time.Second):
			}
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for h.sessionCount() < cfg.TunnelChannels && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := h.sessionCount(); got != cfg.TunnelChannels {
		t.Fatalf("expected %d independent authenticated lanes, connected=%d", cfg.TunnelChannels, got)
	}
	publicListener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go h.acceptPublic(publicListener, "main")
	for i := 0; i < 30; i++ {
		c, e := net.DialTimeout("tcp", publicListener.Addr().String(), time.Second)
		if e != nil {
			t.Fatalf("dial public stream %d: %v", i, e)
		}
		clients = append(clients, c)
	}

	var traffic sync.WaitGroup
	errCh := make(chan error, len(clients))
	for i, c := range clients {
		traffic.Add(1)
		go func(i int, c net.Conn) {
			defer traffic.Done()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			payload := []byte(fmt.Sprintf("live-tls-pool-%02d", i))
			if _, e := c.Write(payload); e != nil {
				errCh <- fmt.Errorf("write stream %d: %w", i, e)
				return
			}
			got := make([]byte, len(payload))
			if _, e := io.ReadFull(c, got); e != nil {
				errCh <- fmt.Errorf("read stream %d: %w", i, e)
				return
			}
			if string(got) != string(payload) {
				errCh <- fmt.Errorf("stream %d: got %q, want %q", i, got, payload)
			}
		}(i, c)
	}
	traffic.Wait()
	close(errCh)
	for e := range errCh {
		t.Error(e)
	}

	h.mu.RLock()
	activeLanes := make([]*hubSession, 0, len(h.sessions))
	for lane := range h.sessions {
		activeLanes = append(activeLanes, lane)
	}
	h.mu.RUnlock()
	if len(activeLanes) != 2 {
		t.Fatalf("expected both TLS lanes to remain active, got %d", len(activeLanes))
	}
	if activeLanes[0].activeStreams() == 0 || activeLanes[1].activeStreams() == 0 {
		t.Fatalf("the 30 client streams were not distributed over both lanes: %d and %d", activeLanes[0].activeStreams(), activeLanes[1].activeStreams())
	}
}
