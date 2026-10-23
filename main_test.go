package main

import (
	"io"
	"net"
	"strings"
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
