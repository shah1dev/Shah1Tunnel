package main

import (
	"bytes"
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

type firstWriteGateConn struct {
	net.Conn
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *firstWriteGateConn) Write(p []byte) (int, error) {
	first := false
	c.once.Do(func() {
		first = true
		close(c.started)
	})
	if first {
		<-c.release
	}
	return c.Conn.Write(p)
}

func TestWirePrioritizesControlFramesOverQueuedData(t *testing.T) {
	a, b := net.Pipe()
	gated := &firstWriteGateConn{Conn: a, started: make(chan struct{}), release: make(chan struct{})}
	wa := newWire(gated)
	wb := newWire(b)
	defer wa.close()
	defer wb.close()

	firstDone := make(chan error, 1)
	go func() { firstDone <- wa.send(msgData, 1, []byte("first-data")) }()
	select {
	case <-gated.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start first data frame")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- wa.send(msgData, 2, []byte("second-data")) }()
	deadline := time.Now().Add(time.Second)
	for len(wa.dataQ) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(wa.dataQ) == 0 {
		t.Fatal("second data frame was not queued")
	}

	controlDone := make(chan error, 1)
	go func() { controlDone <- wa.send(msgOpenOK, 3, nil) }()
	deadline = time.Now().Add(time.Second)
	for len(wa.controlQ) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(wa.controlQ) == 0 {
		t.Fatal("control frame was not queued")
	}
	close(gated.release)

	want := []struct {
		typ  byte
		id   uint32
		data string
	}{
		{msgData, 1, "first-data"},
		{msgOpenOK, 3, ""},
		{msgData, 2, "second-data"},
	}
	for i, w := range want {
		got, err := wb.receive()
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if got.typ != w.typ || got.id != w.id || string(got.data) != w.data {
			t.Fatalf("frame %d = type %d id %d data %q; want type %d id %d data %q", i, got.typ, got.id, got.data, w.typ, w.id, w.data)
		}
	}
	for name, ch := range map[string]<-chan error{"first data": firstDone, "control": controlDone, "second data": secondDone} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("%s send: %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s send did not complete", name)
		}
	}
}

func TestWireWriteFailureClosesLane(t *testing.T) {
	a, b := net.Pipe()
	w := newWire(a)
	_ = b.Close()
	defer w.close()
	if err := w.send(msgData, 1, []byte("partial-frame-must-not-be-reused")); err == nil {
		t.Fatal("expected a failed write on closed peer")
	}
	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("write error did not close the framing lane")
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

func TestAgentReceiveLoopDoesNotBlockOnSlowTarget(t *testing.T) {
	localWire, peerConn := net.Pipe()
	s := newAgentSession(localWire, map[string]string{})
	peer := newWire(peerConn)
	defer peer.close()

	slowDest, slowPeer := net.Pipe()
	defer slowPeer.Close() // Intentionally never read: its writer must block.
	fastDest, fastPeer := net.Pipe()
	defer fastPeer.Close()

	makeStream := func(id uint32, conn net.Conn) *agentStream {
		st := &agentStream{conn: conn, inflight: make(chan struct{}, streamWindowFrames)}
		st.writer = newStreamWriter(conn,
			func() error { return s.wire.send(msgDataAck, id, nil) },
			func() {
				if st.eof.mark(false) {
					s.remove(id, true)
				}
			},
			func(err error) {
				if s.remove(id, true) != nil {
					_ = s.wire.send(msgClose, id, nil)
				}
			},
		)
		return st
	}
	slow := makeStream(1, slowDest)
	fast := makeStream(2, fastDest)
	s.mu.Lock()
	s.streams[1] = slow
	s.streams[2] = fast
	s.mu.Unlock()
	go s.run()
	t.Cleanup(s.shutdown)

	// Start the fast destination reader before sending; net.Pipe intentionally
	// applies zero-buffer back-pressure, so the slow stream remains blocked.
	fastRead := make(chan string, 1)
	go func() {
		b := make([]byte, 4)
		if _, err := io.ReadFull(fastPeer, b); err != nil {
			fastRead <- "ERROR: " + err.Error()
			return
		}
		fastRead <- string(b)
	}()
	if err := peer.send(msgData, 1, make([]byte, 32*1024)); err != nil {
		t.Fatalf("send slow stream frame: %v", err)
	}
	if err := peer.send(msgData, 2, []byte("FAST")); err != nil {
		t.Fatalf("send healthy stream frame while slow writer is blocked: %v", err)
	}
	select {
	case got := <-fastRead:
		if got != "FAST" {
			t.Fatalf("healthy stream received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow target blocked the shared receive loop; healthy stream stalled")
	}
	_ = peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	f, err := peer.receive()
	if err != nil {
		t.Fatalf("receive ACK for healthy stream while slow writer is blocked: %v", err)
	}
	if f.typ != msgDataAck || f.id != 2 {
		t.Fatalf("expected ACK for stream 2, got type=%d id=%d", f.typ, f.id)
	}
	select {
	case <-s.wire.done:
		t.Fatal("one slow target incorrectly closed the whole tunnel lane")
	default:
	}
}

func TestReverseStreamIntegrationBeyondFlowControlWindow(t *testing.T) {
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

	a, b := net.Pipe()
	hs := newHubSession(a)
	as := newAgentSession(b, map[string]string{"main": echo.Addr().String()})
	go hs.run()
	go as.run()
	defer hs.shutdown()
	defer as.shutdown()

	tunnelSide, client := net.Pipe()
	defer client.Close()
	if _, _, err := hs.addStream(tunnelSide, "main"); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	_ = client.SetDeadline(time.Now().Add(15 * time.Second))
	unit := []byte("independent-stream-credit-window/")
	payload := bytes.Repeat(unit, (2<<20)/len(unit)+1) // More than eight 32-KiB frames.
	writeDone := make(chan error, 1)
	go func() { _, e := client.Write(payload); writeDone <- e }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatalf("read large echo transfer: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("large echo payload changed in transit")
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write large echo transfer: %v", err)
	}
}

func TestReverseTunnelPreservesTCPHalfClose(t *testing.T) {
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()
	go func() {
		for {
			conn, e := targetListener.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				request, _ := io.ReadAll(c) // Complete only after CloseWrite is forwarded.
				_, _ = c.Write(append([]byte("reply:"), request...))
			}(conn)
		}
	}()

	hubWire, agentWire := net.Pipe()
	hs := newHubSession(hubWire)
	as := newAgentSession(agentWire, map[string]string{"main": targetListener.Addr().String()})
	go hs.run()
	go as.run()
	defer hs.shutdown()
	defer as.shutdown()

	publicListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer publicListener.Close()
	serverSideCh := make(chan net.Conn, 1)
	go func() {
		c, e := publicListener.Accept()
		if e == nil {
			serverSideCh <- c
		}
	}()
	client, err := net.DialTimeout("tcp", publicListener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var serverSide net.Conn
	select {
	case serverSide = <-serverSideCh:
	case <-time.After(time.Second):
		t.Fatal("public TCP accept timed out")
	}
	defer serverSide.Close()
	if _, _, err := hs.addStream(serverSide, "main"); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte("request-body")); err != nil {
		t.Fatal(err)
	}
	if err := client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read response after half-close: %v", err)
	}
	if string(got) != "reply:request-body" {
		t.Fatalf("got %q, want %q", got, "reply:request-body")
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
	if got := authProtocol("RREV3 "+token+"\n", token); got != "RREV3" {
		t.Fatalf("current protocol rejected: %q", got)
	}
	if validAuth("RREV2 "+token+"\n", token) || validAuth("RREV1 "+token+"\n", token) {
		t.Fatal("obsolete protocol accepted")
	}
	if validAuth("RREV3 "+strings.Repeat("b", 64), token) {
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

func TestGeoDisplayKeepsIPv4WhenCountryLookupFails(t *testing.T) {
	g := ServerGeo{IP: "94.101.187.48", UpdatedAt: time.Now(), LastAttempt: time.Now(), Valid: true}
	ip, country := geoDisplay(g)
	if ip != "94.101.187.48" || country != "Unknown" {
		t.Fatalf("a valid IPv4 must remain visible when GeoIP fails: ip=%q country=%q", ip, country)
	}
}

func TestIsPublicIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"94.101.187.48", true},
		{"8.8.8.8", true},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"100.64.0.1", false},
		{"127.0.0.1", false},
		{"169.254.1.1", false},
		{"203.0.113.9", false},
		{"2a01:4f8:c010:33c3::1", false},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			got := isPublicIPv4(net.ParseIP(tc.ip))
			if got != tc.want {
				t.Fatalf("isPublicIPv4(%q)=%v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestParseGeoResponseMapsIranCountryCode(t *testing.T) {
	body := []byte(`{"ip":"94.101.187.48","country":"IR","country_code":"IR","success":true}`)
	g, ok := parseGeoResponse(body, "94.101.187.48")
	if !ok || !g.Valid || g.IP != "94.101.187.48" || g.Country != "Iran" || g.CountryCode != "IR" {
		t.Fatalf("unexpected parsed GeoIP result: ok=%v geo=%#v", ok, g)
	}
}

func TestParseGeoResponseRejectsMismatchedIP(t *testing.T) {
	body := []byte(`{"ip":"8.8.8.8","country_name":"United States","country_code":"US"}`)
	if _, ok := parseGeoResponse(body, "94.101.187.48"); ok {
		t.Fatal("GeoIP result for a different address must be rejected")
	}
}

func TestParseGeoResponseMapsIranWhenOnlyCountryCodeIsAvailable(t *testing.T) {
	body := []byte(`{"ip":"94.101.187.48","country":"IR"}`)
	g, ok := parseGeoResponse(body, "94.101.187.48")
	if !ok || g.Country != "Iran" || g.CountryCode != "IR" {
		t.Fatalf("country-code-only provider should map IR to Iran: ok=%v geo=%#v", ok, g)
	}
}
