package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	version         = "1.3.3"
	configPath      = "/etc/ravanreverse/config.json"
	maxFrame        = 64 * 1024
	msgOpen    byte = 1
	msgOpenOK  byte = 2
	msgOpenErr byte = 3
	msgData    byte = 4
	msgClose   byte = 5
	msgPing    byte = 6
	msgPong    byte = 7
	maxStreams      = 512
)

type PortMapping struct {
	ID     string `json:"id"`
	Listen string `json:"listen,omitempty"` // Hub public listener
	Target string `json:"target,omitempty"` // Agent local target
}

type Config struct {
	Version       string        `json:"version"`
	Role          string        `json:"role"` // hub on Iran, agent on outside
	ControlListen string        `json:"control_listen,omitempty"`
	PublicListen  string        `json:"public_listen,omitempty"`
	HubAddr       string        `json:"hub_addr,omitempty"`
	Target        string        `json:"target,omitempty"`
	Token         string        `json:"token"`
	CertFile      string        `json:"cert_file,omitempty"`
	KeyFile       string        `json:"key_file,omitempty"`
	Fingerprint   string        `json:"fingerprint,omitempty"`
	Ports         []PortMapping `json:"ports,omitempty"`
}

type RuntimeSnapshot struct {
	Role              string    `json:"role"`
	Updated           time.Time `json:"updated"`
	Connected         bool      `json:"connected"`
	ActiveConnections int64     `json:"active_connections"`
	TotalConnections  uint64    `json:"total_connections"`
	BytesSent         uint64    `json:"bytes_sent"`
	BytesReceived     uint64    `json:"bytes_received"`
}

var metricActive atomic.Int64
var metricTotal atomic.Uint64
var metricSent atomic.Uint64
var metricReceived atomic.Uint64
var metricConnected atomic.Bool
var lastAuthFailureLog atomic.Int64

// logAuthFailure rate-limits unauthenticated peer logs to reduce journal flooding.
func logAuthFailure(message string) {
	now := time.Now().UnixNano()
	previous := lastAuthFailureLog.Load()
	if now-previous < int64(5*time.Second) {
		return
	}
	if lastAuthFailureLog.CompareAndSwap(previous, now) {
		log.Printf("%s", message)
	}
}

func startMetricsReporter(role string) {
	writeRuntimeStats(role)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for range t.C {
			writeRuntimeStats(role)
		}
	}()
}
func writeRuntimeStats(role string) {
	s := RuntimeSnapshot{Role: role, Updated: time.Now().UTC(), Connected: metricConnected.Load(), ActiveConnections: metricActive.Load(), TotalConnections: metricTotal.Load(), BytesSent: metricSent.Load(), BytesReceived: metricReceived.Load()}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	tmp := "/etc/ravanreverse/runtime.json.tmp"
	if err = os.MkdirAll("/etc/ravanreverse", 0700); err != nil {
		return
	}
	if err = os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, "/etc/ravanreverse/runtime.json")
	_ = os.Chmod("/etc/ravanreverse/runtime.json", 0600)
}

type counterWriter struct {
	w       io.Writer
	counter *atomic.Uint64
}

func (w counterWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		w.counter.Add(uint64(n))
	}
	return n, err
}

type frame struct {
	typ  byte
	id   uint32
	data []byte
}

type wire struct {
	conn    net.Conn
	writeMu sync.Mutex
	done    chan struct{}
	once    sync.Once
}

func newWire(c net.Conn) *wire { return &wire{conn: c, done: make(chan struct{})} }
func (w *wire) close()         { w.once.Do(func() { close(w.done); _ = w.conn.Close() }) }
func (w *wire) send(typ byte, id uint32, data []byte) error {
	if len(data) > maxFrame {
		return fmt.Errorf("frame too large: %d", len(data))
	}
	select {
	case <-w.done:
		return net.ErrClosed
	default:
	}
	var h [9]byte
	h[0] = typ
	binary.BigEndian.PutUint32(h[1:5], id)
	binary.BigEndian.PutUint32(h[5:9], uint32(len(data)))
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if err := writeFull(w.conn, h[:]); err != nil {
		return err
	}
	if len(data) > 0 {
		return writeFull(w.conn, data)
	}
	return nil
}
func (w *wire) receive() (frame, error) {
	var h [9]byte
	if _, err := io.ReadFull(w.conn, h[:]); err != nil {
		return frame{}, err
	}
	n := binary.BigEndian.Uint32(h[5:9])
	if n > maxFrame {
		return frame{}, fmt.Errorf("peer sent oversized frame: %d", n)
	}
	f := frame{typ: h[0], id: binary.BigEndian.Uint32(h[1:5])}
	if n > 0 {
		f.data = make([]byte, int(n))
		if _, err := io.ReadFull(w.conn, f.data); err != nil {
			return frame{}, err
		}
	}
	return f, nil
}
func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func heartbeat(w *wire, stop <-chan struct{}) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-w.done:
			return
		case <-t.C:
			if err := w.send(msgPing, 0, nil); err != nil {
				w.close()
				return
			}
		}
	}
}

type hubStream struct {
	conn       net.Conn
	opened     chan error
	openedOnce sync.Once
}

func (s *hubStream) reportOpen(err error) { s.openedOnce.Do(func() { s.opened <- err }) }

type hubSession struct {
	wire     *wire
	mu       sync.RWMutex
	streams  map[uint32]*hubStream
	next     atomic.Uint32
	stopOnce sync.Once
}

func newHubSession(c net.Conn) *hubSession {
	return &hubSession{wire: newWire(c), streams: make(map[uint32]*hubStream)}
}
func (s *hubSession) get(id uint32) *hubStream {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.streams[id]
}
func (s *hubSession) remove(id uint32, closeConn bool) *hubStream {
	s.mu.Lock()
	st := s.streams[id]
	delete(s.streams, id)
	s.mu.Unlock()
	if st != nil {
		metricActive.Add(-1)
	}
	if st != nil && closeConn {
		_ = st.conn.Close()
		st.reportOpen(errors.New("stream closed"))
	}
	return st
}
func (s *hubSession) shutdown() {
	s.stopOnce.Do(func() {
		s.wire.close()
		s.mu.Lock()
		for id, st := range s.streams {
			_ = st.conn.Close()
			st.reportOpen(net.ErrClosed)
			delete(s.streams, id)
			metricActive.Add(-1)
		}
		s.mu.Unlock()
	})
}
func (s *hubSession) addStream(c net.Conn, portID ...string) (uint32, *hubStream, error) {
	key := "main"
	if len(portID) > 0 && strings.TrimSpace(portID[0]) != "" {
		key = portID[0]
	}
	id := s.next.Add(1)
	if id == 0 {
		id = s.next.Add(1)
	}
	st := &hubStream{conn: c, opened: make(chan error, 1)}
	s.mu.Lock()
	select {
	case <-s.wire.done:
		s.mu.Unlock()
		return 0, nil, net.ErrClosed
	default:
	}
	if len(s.streams) >= maxStreams {
		s.mu.Unlock()
		return 0, nil, errors.New("maximum concurrent stream limit reached")
	}
	s.streams[id] = st
	metricActive.Add(1)
	metricTotal.Add(1)
	s.mu.Unlock()
	if err := s.wire.send(msgOpen, id, []byte(key)); err != nil {
		s.remove(id, true)
		return 0, nil, err
	}
	select {
	case err := <-st.opened:
		if err != nil {
			s.remove(id, true)
			return 0, nil, err
		}
	case <-time.After(10 * time.Second):
		s.remove(id, true)
		_ = s.wire.send(msgClose, id, nil)
		return 0, nil, errors.New("remote target did not open within 10 seconds")
	case <-s.wire.done:
		s.remove(id, true)
		return 0, nil, net.ErrClosed
	}
	go s.pumpPublicToAgent(id, st)
	return id, st, nil
}
func (s *hubSession) pumpPublicToAgent(id uint32, st *hubStream) {
	buf := make([]byte, 32*1024)
	for {
		n, err := st.conn.Read(buf)
		if n > 0 {
			if e := s.wire.send(msgData, id, buf[:n]); e != nil {
				break
			}
			metricSent.Add(uint64(n))
		}
		if err != nil {
			break
		}
	}
	_ = s.wire.send(msgClose, id, nil)
	s.remove(id, true)
}
func (s *hubSession) run() {
	defer s.shutdown()
	stop := make(chan struct{})
	defer close(stop)
	go heartbeat(s.wire, stop)
	for {
		_ = s.wire.conn.SetReadDeadline(time.Now().Add(65 * time.Second))
		f, err := s.wire.receive()
		if err != nil {
			log.Printf("reverse agent session ended (connection closed or timed out)")
			return
		}
		st := s.get(f.id)
		switch f.typ {
		case msgOpenOK:
			if st != nil {
				st.reportOpen(nil)
			}
		case msgOpenErr:
			if st != nil {
				msg := strings.TrimSpace(string(f.data))
				if len(msg) > 200 {
					msg = msg[:200]
				}
				st.reportOpen(fmt.Errorf("outside target open failed: %s", msg))
			}
		case msgData:
			if st != nil {
				if err := writeFull(st.conn, f.data); err != nil {
					s.remove(f.id, true)
					_ = s.wire.send(msgClose, f.id, nil)
				} else {
					metricReceived.Add(uint64(len(f.data)))
				}
			}
		case msgClose:
			s.remove(f.id, true)
		case msgPing:
			if err := s.wire.send(msgPong, 0, nil); err != nil {
				return
			}
		case msgPong:
			// Heartbeat acknowledged.
		default:
			return
		}
	}
}

type agentStream struct {
	conn net.Conn
	once sync.Once
}

func (s *agentStream) close() { s.once.Do(func() { _ = s.conn.Close() }) }

type agentSession struct {
	wire     *wire
	targets  map[string]string
	mu       sync.RWMutex
	streams  map[uint32]*agentStream
	stopOnce sync.Once
}

func newAgentSession(c net.Conn, targets map[string]string) *agentSession {
	cp := make(map[string]string, len(targets))
	for k, v := range targets {
		cp[k] = v
	}
	return &agentSession{wire: newWire(c), targets: cp, streams: make(map[uint32]*agentStream)}
}
func (s *agentSession) get(id uint32) *agentStream {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.streams[id]
}
func (s *agentSession) remove(id uint32, closeConn bool) *agentStream {
	s.mu.Lock()
	st := s.streams[id]
	delete(s.streams, id)
	s.mu.Unlock()
	if st != nil {
		metricActive.Add(-1)
	}
	if st != nil && closeConn {
		st.close()
	}
	return st
}
func (s *agentSession) shutdown() {
	s.stopOnce.Do(func() {
		s.wire.close()
		s.mu.Lock()
		for id, st := range s.streams {
			st.close()
			delete(s.streams, id)
			metricActive.Add(-1)
		}
		s.mu.Unlock()
	})
}
func (s *agentSession) openTarget(id uint32, key string) {
	target, ok := s.targets[key]
	if !ok {
		_ = s.wire.send(msgOpenErr, id, []byte("unknown port mapping"))
		return
	}
	c, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		log.Printf("local target unavailable for mapping %q (details suppressed)", key)
		_ = s.wire.send(msgOpenErr, id, []byte("local target unavailable"))
		return
	}
	st := &agentStream{conn: c}
	s.mu.Lock()
	select {
	case <-s.wire.done:
		s.mu.Unlock()
		st.close()
		return
	default:
	}
	if len(s.streams) >= maxStreams {
		s.mu.Unlock()
		st.close()
		_ = s.wire.send(msgOpenErr, id, []byte("maximum concurrent stream limit reached"))
		return
	}
	s.streams[id] = st
	metricActive.Add(1)
	metricTotal.Add(1)
	s.mu.Unlock()
	if err := s.wire.send(msgOpenOK, id, nil); err != nil {
		s.remove(id, true)
		return
	}
	go s.pumpTargetToHub(id, st)
}
func (s *agentSession) pumpTargetToHub(id uint32, st *agentStream) {
	buf := make([]byte, 32*1024)
	for {
		n, err := st.conn.Read(buf)
		if n > 0 {
			if e := s.wire.send(msgData, id, buf[:n]); e != nil {
				break
			}
			metricSent.Add(uint64(n))
		}
		if err != nil {
			break
		}
	}
	_ = s.wire.send(msgClose, id, nil)
	s.remove(id, true)
}
func (s *agentSession) run() {
	defer s.shutdown()
	stop := make(chan struct{})
	defer close(stop)
	go heartbeat(s.wire, stop)
	for {
		_ = s.wire.conn.SetReadDeadline(time.Now().Add(65 * time.Second))
		f, err := s.wire.receive()
		if err != nil {
			return
		}
		switch f.typ {
		case msgOpen:
			go s.openTarget(f.id, string(f.data))
		case msgData:
			st := s.get(f.id)
			if st != nil {
				if err := writeFull(st.conn, f.data); err != nil {
					s.remove(f.id, true)
					_ = s.wire.send(msgClose, f.id, nil)
				} else {
					metricReceived.Add(uint64(len(f.data)))
				}
			}
		case msgClose:
			s.remove(f.id, true)
		case msgPing:
			if err := s.wire.send(msgPong, 0, nil); err != nil {
				return
			}
		case msgPong:
			// Heartbeat acknowledged.
		default:
			return
		}
	}
}

type hubServer struct {
	cfg    Config
	mu     sync.RWMutex
	active *hubSession
}

func (h *hubServer) setActive(s *hubSession) {
	metricConnected.Store(true)
	h.mu.Lock()
	old := h.active
	h.active = s
	h.mu.Unlock()
	if old != nil && old != s {
		old.shutdown()
	}
}
func (h *hubServer) clearActive(s *hubSession) {
	h.mu.Lock()
	if h.active == s {
		h.active = nil
		metricConnected.Store(false)
	}
	h.mu.Unlock()
}
func (h *hubServer) current() *hubSession { h.mu.RLock(); defer h.mu.RUnlock(); return h.active }
func (h *hubServer) acceptPublic(ln net.Listener, portID string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("public listener accept: %v", err)
			continue
		}
		s := h.current()
		if s == nil {
			// Drop quietly while the Agent is disconnected to avoid log amplification.
			_ = c.Close()
			continue
		}
		go func() {
			if _, _, err := s.addStream(c, portID); err != nil {
				log.Printf("open reverse stream: %v", err)
				_ = c.Close()
			}
		}()
	}
}
func (h *hubServer) serveAgent(raw net.Conn, tlsCfg *tls.Config) {
	tc := tls.Server(raw, tlsCfg)
	defer tc.Close()
	_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
	if err := tc.Handshake(); err != nil {
		logAuthFailure("agent TLS handshake failed (details suppressed)")
		return
	}
	r := bufio.NewReaderSize(tc, 256)
	lineBytes, err := r.ReadSlice('\n')
	// A token handshake is under 100 bytes. Reject oversized lines instead
	// of allowing a peer to force unbounded allocation before authentication.
	if errors.Is(err, bufio.ErrBufferFull) {
		logAuthFailure("rejected oversized reverse agent handshake")
		return
	}
	protocol := authProtocol(string(lineBytes), h.cfg.Token)
	if err != nil || protocol == "" {
		_, _ = tc.Write([]byte("NO\n"))
		logAuthFailure("rejected reverse agent authentication")
		return
	}
	if _, err = tc.Write([]byte("OK\n")); err != nil {
		return
	}
	_ = tc.SetDeadline(time.Time{})
	// Keep reader buffering out of the framed protocol. The client waits for OK before sending frames.
	s := newHubSession(tc)
	h.setActive(s)
	defer h.clearActive(s)
	log.Printf("outside agent connected")
	s.run()
	log.Printf("outside agent disconnected")
}
func runHub(cfg Config) error {
	cfg = normalizeConfig(cfg)
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("load hub certificate/key: %w; run setup again", err)
	}
	control, err := net.Listen("tcp", cfg.ControlListen)
	if err != nil {
		return fmt.Errorf("listen control %s: %w", cfg.ControlListen, err)
	}
	defer control.Close()
	type listenerMap struct {
		listener net.Listener
		id       string
	}
	listeners := make([]listenerMap, 0, len(cfg.Ports))
	for _, p := range cfg.Ports {
		ln, e := net.Listen("tcp", p.Listen)
		if e != nil {
			for _, opened := range listeners {
				_ = opened.listener.Close()
			}
			return fmt.Errorf("listen public mapping %q (%s): %w", p.ID, p.Listen, e)
		}
		listeners = append(listeners, listenerMap{listener: ln, id: p.ID})
	}
	defer func() {
		for _, p := range listeners {
			_ = p.listener.Close()
		}
	}()
	h := &hubServer{cfg: cfg}
	startMetricsReporter("hub")
	for _, p := range listeners {
		go h.acceptPublic(p.listener, p.id)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	log.Printf("HUB listening for agent on configured control port; public mappings=%d", len(listeners))
	// Bound concurrent unauthenticated control connections so a burst of
	// slow handshakes cannot create an unbounded number of goroutines.
	controlSlots := make(chan struct{}, 16)
	for {
		c, err := control.Accept()
		if err != nil {
			return fmt.Errorf("accept control: %w", err)
		}
		select {
		case controlSlots <- struct{}{}:
			go func(conn net.Conn) {
				defer func() { <-controlSlots }()
				h.serveAgent(conn, tlsCfg)
			}(c)
		default:
			_ = c.Close()
		}
	}
}

func runRelay(cfg Config) error {
	cfg = normalizeConfig(cfg)
	type relayListener struct {
		ln         net.Listener
		target, id string
	}
	listeners := make([]relayListener, 0, len(cfg.Ports))
	for _, p := range cfg.Ports {
		ln, err := net.Listen("tcp", p.Listen)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.ln.Close()
			}
			return fmt.Errorf("listen Relay mapping %q: %w", p.ID, err)
		}
		listeners = append(listeners, relayListener{ln: ln, target: p.Target, id: p.ID})
	}
	defer func() {
		for _, p := range listeners {
			_ = p.ln.Close()
		}
	}()
	startMetricsReporter("relay")
	metricConnected.Store(true)
	limit := make(chan struct{}, maxStreams)
	for _, p := range listeners {
		p := p
		go func() {
			for {
				c, err := p.ln.Accept()
				if err != nil {
					log.Printf("Relay listener stopped for mapping %q", p.id)
					return
				}
				select {
				case limit <- struct{}{}:
					metricActive.Add(1)
					metricTotal.Add(1)
					go func(client net.Conn) { defer func() { <-limit; metricActive.Add(-1) }(); relayTCP(client, p.target) }(c)
				default:
					_ = c.Close()
				}
			}
		}()
	}
	log.Printf("RELAY ready with %d mapping(s); endpoint addresses suppressed", len(listeners))
	select {}
}

func relayTCP(client net.Conn, target string) {
	d := net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	upstream, err := d.Dial("tcp", target)
	if err != nil {
		log.Printf("Relay upstream unavailable (details suppressed)")
		_ = client.Close()
		return
	}
	defer client.Close()
	defer upstream.Close()
	finished := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(counterWriter{w: upstream, counter: &metricSent}, client)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		finished <- struct{}{}
	}()
	_, _ = io.Copy(counterWriter{w: client, counter: &metricReceived}, upstream)
	if tcp, ok := client.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
	}
}

func authProtocol(line, token string) string {
	line = strings.TrimSpace(line)
	const prefix = "RREV2 "
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	got := strings.TrimPrefix(line, prefix)
	if len(got) == len(token) && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
		return "RREV2"
	}
	return ""
}
func validAuth(line, token string) bool { return authProtocol(line, token) != "" }

func runAgent(cfg Config) error {
	pin, err := normalizeFingerprint(cfg.Fingerprint)
	if err != nil {
		return err
	}
	backoff := time.Second
	startMetricsReporter("agent")
	for {
		attemptStarted := time.Now()
		if err := connectAndRunAgent(cfg, pin); err != nil {
			metricConnected.Store(false)
			// A session that survived a minute is considered stable; start recovery
			// quickly after a later drop instead of retaining a stale long backoff.
			if time.Since(attemptStarted) >= time.Minute {
				backoff = time.Second
			}
			msg := err.Error()
			msg = strings.ReplaceAll(msg, cfg.HubAddr, "[hub-address-redacted]")
			for _, p := range normalizeConfig(cfg).Ports {
				msg = strings.ReplaceAll(msg, p.Target, "[target-redacted]")
			}
			log.Printf("reverse connection failed: %s", msg)
		}
		delay := jitteredBackoff(backoff)
		log.Printf("retrying hub connection in %s (with jitter)", delay)
		time.Sleep(delay)
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// jitteredBackoff prevents many agents from retrying in lockstep after a shared outage.
// The delay stays within +/-20% of the base interval.
func jitteredBackoff(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	span := int64(base / 5)
	if span <= 0 {
		return base
	}
	n, err := rand.Int(rand.Reader, big.NewInt(2*span+1))
	if err != nil {
		return base
	}
	return base - time.Duration(span) + time.Duration(n.Int64())
}

func connectAndRunAgent(cfg Config, pin []byte) error {
	d := net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}, VerifyConnection: func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("hub presented no certificate")
		}
		sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
			return errors.New("hub TLS certificate fingerprint mismatch")
		}
		return nil
	}}
	c, err := tls.DialWithDialer(&d, "tcp", cfg.HubAddr, tlsCfg)
	if err != nil {
		return err
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if err = writeFull(c, []byte("RREV2 "+cfg.Token+"\n")); err != nil {
		_ = c.Close()
		return err
	}
	reply, err := bufio.NewReaderSize(c, 64).ReadString('\n')
	if err != nil || strings.TrimSpace(reply) != "OK" {
		_ = c.Close()
		if err != nil {
			return err
		}
		return errors.New("hub rejected token")
	}
	_ = c.SetDeadline(time.Time{})
	metricConnected.Store(true)
	log.Printf("connected to Hub; forwarding through %d configured port mapping(s)", len(normalizeConfig(cfg).Ports))
	targets := make(map[string]string, len(cfg.Ports))
	for _, p := range normalizeConfig(cfg).Ports {
		targets[p.ID] = p.Target
	}
	s := newAgentSession(c, targets)
	s.run()
	return errors.New("hub connection closed")
}

func normalizeConfig(c Config) Config {
	if len(c.Ports) == 0 {
		if c.Role == "hub" && c.PublicListen != "" {
			c.Ports = []PortMapping{{ID: "main", Listen: c.PublicListen}}
		}
		if c.Role == "agent" && c.Target != "" {
			c.Ports = []PortMapping{{ID: "main", Target: c.Target}}
		}
	}
	if len(c.Ports) > 0 {
		if c.Role == "hub" {
			c.PublicListen = c.Ports[0].Listen
		} else if c.Role == "agent" {
			c.Target = c.Ports[0].Target
		}
	}
	return c
}

func validateConfig(c Config) error {
	c = normalizeConfig(c)
	if c.Version == "" {
		return errors.New("missing config version")
	}
	if c.Role != "hub" && c.Role != "agent" && c.Role != "relay" {
		return errors.New("role must be hub, agent, or relay")
	}
	if len(c.Token) < 48 {
		return errors.New("token must be at least 48 characters")
	}
	if len(c.Ports) == 0 {
		return errors.New("at least one port mapping is required")
	}
	ids, addrs := map[string]bool{}, map[string]bool{}
	if c.Role == "hub" {
		if err := validateTCPAddress("control listener", c.ControlListen); err != nil {
			return err
		}
		if c.CertFile == "" || c.KeyFile == "" {
			return errors.New("cert_file/key_file required for hub")
		}
		_, controlPort, _ := net.SplitHostPort(c.ControlListen)
		for _, p := range c.Ports {
			if !validMappingID(p.ID) {
				return fmt.Errorf("invalid mapping id %q (use letters, numbers, - or _)", p.ID)
			}
			if ids[p.ID] {
				return fmt.Errorf("duplicate mapping id %q", p.ID)
			}
			ids[p.ID] = true
			if err := validateTCPAddress("listen for mapping "+p.ID, p.Listen); err != nil {
				return err
			}
			_, lp, _ := net.SplitHostPort(p.Listen)
			if lp == controlPort {
				return fmt.Errorf("mapping %q uses the same port number as control listener", p.ID)
			}
			if addrs[p.Listen] {
				return fmt.Errorf("duplicate listen address %q", p.Listen)
			}
			addrs[p.Listen] = true
		}
	} else if c.Role == "agent" {
		if err := validateTCPAddress("hub address", c.HubAddr); err != nil {
			return err
		}
		for _, p := range c.Ports {
			if !validMappingID(p.ID) {
				return fmt.Errorf("invalid mapping id %q (use letters, numbers, - or _)", p.ID)
			}
			if ids[p.ID] {
				return fmt.Errorf("duplicate mapping id %q", p.ID)
			}
			ids[p.ID] = true
			if err := validateTCPAddress("target for mapping "+p.ID, p.Target); err != nil {
				return err
			}
		}
		if _, err := normalizeFingerprint(c.Fingerprint); err != nil {
			return err
		}
	} else {
		for _, p := range c.Ports {
			if !validMappingID(p.ID) {
				return fmt.Errorf("invalid mapping id %q (use letters, numbers, - or _)", p.ID)
			}
			if ids[p.ID] {
				return fmt.Errorf("duplicate mapping id %q", p.ID)
			}
			ids[p.ID] = true
			if err := validateTCPAddress("Relay listen for mapping "+p.ID, p.Listen); err != nil {
				return err
			}
			if addrs[p.Listen] {
				return fmt.Errorf("duplicate relay listen address %q", p.Listen)
			}
			addrs[p.Listen] = true
			if err := validateTCPAddress("Iran Hub public target for mapping "+p.ID, p.Target); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateTCPAddress(label, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid %s address: %w", label, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%s host/address cannot be empty; use an explicit IP or hostname", label)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%s port must be a number from 1 to 65535", label)
	}
	return nil
}

func validMappingID(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func normalizeFingerprint(s string) ([]byte, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "sha256:"))
	s = strings.ReplaceAll(s, ":", "")
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		return nil, errors.New("fingerprint must be SHA-256 hex (64 hex chars, colons optional)")
	}
	return b, nil
}
func loadConfig() (Config, error) {
	b, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w; run 'ravanreverse setup' first", configPath, err)
	}
	var c Config
	if err = json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	c = normalizeConfig(c)
	if err = validateConfig(c); err != nil {
		return Config{}, err
	}
	return c, nil
}
func saveConfig(c Config) error {
	c = normalizeConfig(c)
	if err := validateConfig(c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(configPath), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, configPath); err != nil {
		return err
	}
	return os.Chmod(configPath, 0600)
}
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var stdinReader = bufio.NewReader(os.Stdin)
var wizardStep int

const (
	ansiReset  = "\033[0m"
	ansiCyan   = "\033[36m"
	ansiBlue   = "\033[34m"
	ansiPurple = "\033[35m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
)

func paint(code, value string) string {
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return value
	}
	return code + value + ansiReset
}

func uiRule() {
	fmt.Println(paint(ansiBlue, "  ──────────────────────────────────────────────────────────────"))
}

func uiHeader(title, subtitle string) {
	fmt.Println()
	fmt.Println(paint(ansiPurple+ansiBold, "  ╔══════════════════════════════════════════════════════════════╗"))
	fmt.Printf("  %s%s%s\n", paint(ansiPurple+ansiBold, "║"), paint(ansiCyan+ansiBold, centerUI(title, 62)), paint(ansiPurple+ansiBold, "║"))
	fmt.Printf("  %s%s%s\n", paint(ansiPurple+ansiBold, "║"), paint(ansiDim, centerUI(subtitle, 62)), paint(ansiPurple+ansiBold, "║"))
	fmt.Println(paint(ansiPurple+ansiBold, "  ╚══════════════════════════════════════════════════════════════╝"))
}

func centerUI(s string, width int) string {
	if len([]rune(s)) >= width {
		return s
	}
	left := (width - len([]rune(s))) / 2
	right := width - len([]rune(s)) - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

func readAnswer(def string) string {
	v, err := stdinReader.ReadString('\n')
	if err != nil && len(v) == 0 {
		return def
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	return v
}

func ask(prompt, def string) string {
	fmt.Printf("%s", paint(ansiCyan+ansiBold, prompt))
	if def != "" {
		fmt.Printf(" %s", paint(ansiDim, "[default: "+def+"]"))
	}
	fmt.Printf(" %s ", paint(ansiCyan+ansiBold, "›"))
	return readAnswer(def)
}

func wizardAsk(prompt, def string) string {
	wizardStep++
	fmt.Printf("\n  %s %s\n", paint(ansiPurple+ansiBold, fmt.Sprintf("◆ STEP %02d", wizardStep)), paint(ansiBold, prompt))
	if def != "" {
		fmt.Printf("    %s\n", paint(ansiDim, "Suggested: "+def))
	}
	fmt.Printf("    %s ", paint(ansiCyan+ansiBold, "↳"))
	return readAnswer(def)
}

func wizardYesNo(prompt, def string) bool {
	return strings.EqualFold(wizardAsk(prompt, def), "y")
}

func uiSuccess(message string) { fmt.Printf("  %s %s\n", paint(ansiGreen+ansiBold, "✓"), message) }
func uiInfo(message string)    { fmt.Printf("  %s %s\n", paint(ansiCyan, "◇"), message) }
func uiWarn(message string)    { fmt.Printf("  %s %s\n", paint(ansiYellow, "!"), message) }

type uiCardRow struct {
	Text  string
	Color string
}

// uiCard renders a compact, independently colored panel. Width is based on
// plain text so ANSI color codes never distort the alignment.
func uiCard(title string, rows []uiCardRow, borderColor string) {
	width := len([]rune(title))
	if width < 34 {
		width = 34
	}
	for _, row := range rows {
		if n := len([]rune(row.Text)); n > width {
			width = n
		}
	}
	if width > 76 {
		width = 76
	}
	border := func(s string) string { return paint(borderColor, s) }
	fmt.Printf("  %s\n", border("╭"+strings.Repeat("─", width+2)+"╮"))
	titlePadding := width - len([]rune(title))
	fmt.Printf("  %s %s%s %s\n", border("│"), paint(ansiBold, title), strings.Repeat(" ", titlePadding), border("│"))
	for _, row := range rows {
		text := row.Text
		visible := len([]rune(text))
		if visible > width {
			// Long values are wrapped rather than silently truncated.
			for len([]rune(text)) > width {
				runes := []rune(text)
				part := string(runes[:width])
				fmt.Printf("  %s %s%s %s\n", border("│"), paint(row.Color, part), "", border("│"))
				text = string(runes[width:])
			}
			visible = len([]rune(text))
		}
		fmt.Printf("  %s %s%s %s\n", border("│"), paint(row.Color, text), strings.Repeat(" ", width-visible), border("│"))
	}
	fmt.Printf("  %s\n", border("╰"+strings.Repeat("─", width+2)+"╯"))
}
func generateCertificate(certPath, keyPath string) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", err
	}
	tmpl := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Shah Tunnel Hub"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return "", err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(certPath), 0700); err != nil {
		return "", err
	}
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		return "", err
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600); err != nil {
		return "", err
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}
func setup() error {
	wizardStep = 0
	uiHeader("SHAH TUNNEL // SPACE SETUP", "WIZARD   •   SECURE LINK   •   TCP ROUTING")
	uiInfo("Step-by-step setup. Each step asks for one input.")
	if _, err := os.Stat(configPath); err == nil {
		if !wizardYesNo("Overwrite the existing configuration? (y/N)", "n") {
			return errors.New("Setup cancelled; the existing configuration was preserved")
		}
	}

	uiRule()
	fmt.Println(paint(ansiCyan+ansiBold, "  SELECT SERVER ROLE"))
	fmt.Printf("  %s %s\n", paint(ansiGreen+ansiBold, "1"), "🛡️ HUB     | IRAN — accepts Agent connections and exposes public ports")
	fmt.Printf("  %s %s\n", paint(ansiGreen+ansiBold, "2"), "🔗 AGENT   | OUTSIDE — connects securely to Iran and forwards traffic to local services")
	fmt.Printf("  %s %s\n", paint(ansiGreen+ansiBold, "3"), "🌐 RELAY   | THIRD SERVER — public entry point that forwards TCP traffic")
	role := wizardAsk("Select role (1/2/3)", "")
	tok, err := randomHex(32)
	if err != nil {
		return err
	}
	c := Config{Version: version, Token: tok}
	switch role {
	case "1", "hub", "iran":
		c.Role = "hub"
		c.ControlListen = wizardAsk("Agent control listener address", "0.0.0.0:9443")
		c.CertFile, c.KeyFile = "/etc/ravanreverse/hub.crt", "/etc/ravanreverse/hub.key"
		for i := 1; i <= 100; i++ {
			defID := "main"
			if i > 1 {
				defID = fmt.Sprintf("port%d", i)
			}
			id := wizardAsk(fmt.Sprintf("Mapping ID #%d (must match on the Agent)", i), defID)
			listenDefault := "0.0.0.0:443"
			if i > 1 {
				listenDefault = "0.0.0.0:" + strconv.Itoa(8443+i-2)
			}
			addr := wizardAsk(fmt.Sprintf("Public listen address for %s", id), listenDefault)
			c.Ports = append(c.Ports, PortMapping{ID: id, Listen: addr})
			if !wizardYesNo("Add another public port? (y/N)", "n") {
				break
			}
		}
		c = normalizeConfig(c)
		// Validate before rotating certificate/key files, so a typo cannot invalidate
		// a previously working Hub configuration.
		if err = validateConfig(c); err != nil {
			return err
		}
		fp, e := generateCertificate(c.CertFile, c.KeyFile)
		if e != nil {
			return e
		}
		c.Fingerprint = fp
		if err = saveConfig(c); err != nil {
			return err
		}
		uiRule()
		uiCard("✅ HUB CONFIGURED", []uiCardRow{
			{Text: "Configuration saved successfully.", Color: ansiGreen},
		}, ansiGreen+ansiBold)
		uiCard("🔐 AGENT TOKEN", []uiCardRow{
			{Text: c.Token, Color: ansiYellow + ansiBold},
		}, ansiYellow)
		uiCard("🛡️ CERTIFICATE SHA-256 FINGERPRINT", []uiCardRow{
			{Text: c.Fingerprint, Color: ansiCyan + ansiBold},
		}, ansiCyan)
		uiCard("🔌 CONTROL PORT", []uiCardRow{
			{Text: portOf(c.ControlListen), Color: ansiPurple + ansiBold},
		}, ansiPurple)
		mappingRows := make([]uiCardRow, 0, len(c.Ports))
		for _, p := range c.Ports {
			mappingRows = append(mappingRows, uiCardRow{Text: fmt.Sprintf("ID=%s   LISTEN=%s", p.ID, p.Listen), Color: ansiCyan})
		}
		uiCard("🌐 PUBLIC PORT MAPPINGS", mappingRows, ansiBlue)
		uiCard("⚠️ SECURITY REMINDER", []uiCardRow{
			{Text: "Keep the token secret; enter it only on the Agent.", Color: ansiYellow + ansiBold},
			{Text: "Copy the exact certificate fingerprint into the Agent setup.", Color: ansiDim},
		}, ansiYellow)
	case "2", "agent", "outside":
		c.Role = "agent"
		c.HubAddr = wizardAsk("Reachable Iran Hub address as IP:PORT (control port)", "")
		c.Token = wizardAsk("Token shown on the Iran Hub", "")
		c.Fingerprint = wizardAsk("Hub certificate SHA-256 fingerprint", "")
		for i := 1; i <= 100; i++ {
			defID := "main"
			if i > 1 {
				defID = fmt.Sprintf("port%d", i)
			}
			id := wizardAsk(fmt.Sprintf("Mapping ID #%d (must match the Hub)", i), defID)
			targetDefault := "127.0.0.1:443"
			if i > 1 {
				targetDefault = ""
			}
			target := wizardAsk(fmt.Sprintf("Local target for mapping %s on the outside server (IP:PORT)", id), targetDefault)
			if strings.TrimSpace(target) == "" {
				return fmt.Errorf("target for mapping %q is required", id)
			}
			c.Ports = append(c.Ports, PortMapping{ID: id, Target: target})
			if !wizardYesNo("Add another target mapping? (y/N)", "n") {
				break
			}
		}
		c = normalizeConfig(c)
		if err = saveConfig(c); err != nil {
			return err
		}
		_ = os.Remove("/etc/ravanreverse/hub.key")
		_ = os.Remove("/etc/ravanreverse/hub.crt")
		uiSuccess("Agent configuration saved. Reconnection is automatic if the link drops.")
	case "3", "relay", "front":
		c.Role = "relay"
		c.Token = "relay-mode-not-used-" + tok
		for i := 1; i <= 100; i++ {
			defID := "front"
			if i > 1 {
				defID = fmt.Sprintf("front%d", i)
			}
			id := wizardAsk(fmt.Sprintf("Relay mapping ID #%d", i), defID)
			listenDefault := "0.0.0.0:443"
			if i > 1 {
				listenDefault = "0.0.0.0:" + strconv.Itoa(8443+i-2)
			}
			listen := wizardAsk(fmt.Sprintf("Public client listener for %s", id), listenDefault)
			target := wizardAsk(fmt.Sprintf("Reachable Iran-side target for %s (IP:PORT)", id), "")
			c.Ports = append(c.Ports, PortMapping{ID: id, Listen: listen, Target: target})
			if !wizardYesNo("Add another Relay port? (y/N)", "n") {
				break
			}
		}
		c = normalizeConfig(c)
		if err = saveConfig(c); err != nil {
			return err
		}
		_ = os.Remove("/etc/ravanreverse/hub.key")
		_ = os.Remove("/etc/ravanreverse/hub.crt")
		uiSuccess("Relay configured.")
		uiWarn("Relay changes the client entry point only. The Relay operator and network path may still see the Iran destination IP.")
	default:
		return errors.New("Invalid role. Enter 1 for HUB, 2 for Agent, or 3 for Relay.")
	}
	fmt.Printf("\n  %s %s\n", paint(ansiGreen+ansiBold, "✓"), "Configuration saved: "+configPath)
	uiInfo("Enabling the service and automatic startup after VPS reboot…")
	if err := ensureTunnelRunning(true); err != nil {
		return fmt.Errorf("configuration was saved, but automatic startup failed: %w", err)
	}
	uiSuccess("Tunnel is active and configured to start automatically after reboot.")
	return nil
}

func portOf(addr string) string {
	_, p, e := net.SplitHostPort(addr)
	if e != nil {
		return "?"
	}
	return p
}

func isRoot() bool { return os.Geteuid() == 0 }
func ensureRoot() bool {
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "Run as root.")
		return false
	}
	return true
}
func askYN(prompt string, def string) bool { return strings.EqualFold(ask(prompt, def), "y") }
func runSystemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if len(strings.TrimSpace(string(out))) > 0 {
		fmt.Print(string(out))
		if !strings.HasSuffix(string(out), "\n") {
			fmt.Println()
		}
	}
	return err
}
func tunnelIsActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", "ravanreverse").Run() == nil
}

// ensureTunnelRunning enables boot-time startup and verifies the service really became active.
// restartActive reloads freshly-written configuration when setup was just completed.
func ensureTunnelRunning(restartActive bool) error {
	if !isRoot() {
		return errors.New("run as root")
	}
	if _, err := os.Stat("/etc/systemd/system/ravanreverse.service"); err != nil {
		return errors.New("Shah Tunnel is not installed; run the installer first")
	}
	if err := runSystemctl("enable", "ravanreverse"); err != nil {
		return fmt.Errorf("could not enable automatic startup: %w", err)
	}
	var err error
	if tunnelIsActive() {
		if restartActive {
			err = runSystemctl("restart", "ravanreverse")
		} else {
			fmt.Println("Shah Tunnel is already running.")
		}
	} else {
		// Clear a previous failed/start-limit state so a manual start can recover.
		_ = exec.Command("systemctl", "reset-failed", "ravanreverse").Run()
		err = runSystemctl("start", "ravanreverse")
	}
	if err != nil || !tunnelIsActive() {
		showRecentLogs(40)
		if err != nil {
			return fmt.Errorf("service start/restart failed: %w", err)
		}
		return errors.New("service did not reach active state; recent logs are shown above")
	}
	fmt.Println("Shah Tunnel is active; automatic startup at boot is enabled.")
	return nil
}

func showRecentLogs(n int) {
	out, err := exec.Command("journalctl", "-u", "ravanreverse", "-n", strconv.Itoa(n), "--no-pager", "-o", "short-iso").CombinedOutput()
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	if err != nil {
		fmt.Printf("Could not read service logs: %v\n", err)
	}
}

func showStatusAndLogs() error {
	if _, err := os.Stat("/etc/systemd/system/ravanreverse.service"); err != nil {
		return errors.New("Shah Tunnel is not installed")
	}
	out, err := exec.Command("systemctl", "status", "ravanreverse", "--no-pager", "-l").CombinedOutput()
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	fmt.Println("\n--- Recent service logs ---")
	showRecentLogs(40)
	if err != nil && !tunnelIsActive() {
		return errors.New("service is not active; see logs above")
	}
	return nil
}

var errUninstallCancelled = errors.New("uninstall cancelled")

func uninstall() error {
	fmt.Println("This permanently removes only Shah Tunnel files and its own service.")
	fmt.Println("Xray, x-ui, other services and manually-created firewall rules are left untouched.")
	if ask("Type DELETE SHAH TUNNEL to confirm complete removal", "") != "DELETE SHAH TUNNEL" {
		fmt.Println("Cancelled.")
		return errUninstallCancelled
	}

	// Stop and disable only this unit. Do not remove files while systemd still
	// reports the unit as active/deactivating.
	_ = exec.Command("systemctl", "disable", "ravanreverse").Run()
	_ = exec.Command("systemctl", "stop", "ravanreverse").Run()
	state := func() string {
		b, _ := exec.Command("systemctl", "show", "ravanreverse", "-p", "ActiveState", "--value").Output()
		return strings.TrimSpace(string(b))
	}
	for i := 0; i < 20; i++ {
		s := state()
		if s == "" || s == "inactive" || s == "failed" || s == "unknown" || s == "dead" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	s := state()
	if s == "active" || s == "deactivating" || s == "activating" || s == "reloading" {
		// Last-resort signal is scoped to the Shah Tunnel systemd unit only.
		_ = exec.Command("systemctl", "kill", "--kill-who=all", "--signal=KILL", "ravanreverse").Run()
		_ = exec.Command("systemctl", "stop", "ravanreverse").Run()
		time.Sleep(500 * time.Millisecond)
		s = state()
	}
	if s == "active" || s == "deactivating" || s == "activating" || s == "reloading" {
		return fmt.Errorf("service would not stop (state=%s); files were kept. Check: systemctl status ravanreverse", s)
	}

	if err := os.Remove("/etc/systemd/system/ravanreverse.service"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove service unit: %w", err)
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemd daemon-reload failed: %v %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("systemctl", "reset-failed", "ravanreverse").Run()
	for _, path := range []string{"/etc/ravanreverse", "/usr/local/bin/ravanreverse", "/usr/local/bin/shah-tunnel", "/var/backups/ravanreverse", "/var/log/ravanreverse"} {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	if askYN("Also remove known Shah Tunnel installer files under /root? [Y/n]", "y") {
		for _, path := range []string{
			"/root/RavanReverse-1.0.0", "/root/RavanReverse-1.1.0", "/root/RavanReverse-1.2.0", "/root/RavanReverse-1.3.0", "/root/RavanReverse-1.3.1", "/root/RavanReverse-1.3.2", "/root/Shah-Tunnel-1.3.3",
			"/root/RavanReverse-1.0.0-Linux-amd64.zip", "/root/RavanReverse-1.1.0-Linux-amd64.zip", "/root/RavanReverse-1.2.0-Linux-amd64.zip", "/root/RavanReverse-1.3.0-Linux-amd64.zip", "/root/RavanReverse-1.3.1-Linux-amd64.zip", "/root/RavanReverse-1.3.2-Linux-amd64.zip", "/root/Shah-Tunnel-1.3.3-Linux-amd64.zip",
			"/root/RavanReverse-1.1.0-Linux-amd64.zip.sha256", "/root/RavanReverse-1.2.0-Linux-amd64.zip.sha256", "/root/RavanReverse-1.3.0-Linux-amd64.zip.sha256", "/root/RavanReverse-1.3.1-Linux-amd64.zip.sha256", "/root/RavanReverse-1.3.2-Linux-amd64.zip.sha256", "/root/Shah-Tunnel-1.3.3-Linux-amd64.zip.sha256",
		} {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove installer path %s: %w", path, err)
			}
		}
	}
	fmt.Println("Shah Tunnel service, binary and configuration have been removed.")
	fmt.Println("Xray/x-ui and unrelated firewall/proxy settings were not changed.")
	return nil
}

// ServerGeo is a cached public-egress IP and country lookup. It is fetched
// from ipapi.co with a short timeout, cached on disk, and never blocks the tunnel
// service itself. This lookup discloses the server's public egress IP to that API.
type ServerGeo struct {
	IP          string    `json:"ip"`
	Country     string    `json:"country_name"`
	CountryCode string    `json:"country_code"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastAttempt time.Time `json:"last_attempt"`
	Valid       bool      `json:"valid"`
}

const serverGeoCachePath = "/etc/ravanreverse/server-info.json"

func readServerGeoCache() ServerGeo {
	var g ServerGeo
	b, err := os.ReadFile(serverGeoCachePath)
	if err == nil {
		_ = json.Unmarshal(b, &g)
	}
	return g
}

func writeServerGeoCache(g ServerGeo) {
	if err := os.MkdirAll("/etc/ravanreverse", 0700); err != nil {
		return
	}
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return
	}
	tmp := serverGeoCachePath + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		return
	}
	if err := os.Rename(tmp, serverGeoCachePath); err != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = os.Chmod(serverGeoCachePath, 0600)
}

func serverIdentity() (string, string) {
	g := readServerGeoCache()
	now := time.Now()
	if !g.LastAttempt.IsZero() && now.Sub(g.LastAttempt) < 5*time.Minute {
		return geoDisplay(g)
	}
	if g.Valid && !g.UpdatedAt.IsZero() && now.Sub(g.UpdatedAt) < 6*time.Hour {
		return geoDisplay(g)
	}

	g.LastAttempt = now
	client := &http.Client{Timeout: 2500 * time.Millisecond}
	req, err := http.NewRequest(http.MethodGet, "https://ipapi.co/json/", nil)
	if err == nil {
		req.Header.Set("User-Agent", "ShahTunnel/"+version)
		var resp *http.Response
		resp, err = client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var result struct {
					IP          string `json:"ip"`
					Country     string `json:"country_name"`
					CountryAlt  string `json:"country"`
					CountryCode string `json:"country_code"`
				}
				if json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&result) == nil && net.ParseIP(strings.TrimSpace(result.IP)) != nil {
					country := strings.TrimSpace(result.Country)
					if country == "" {
						country = strings.TrimSpace(result.CountryAlt)
					}
					if country != "" {
						g.IP = strings.TrimSpace(result.IP)
						g.Country = country
						g.CountryCode = strings.ToUpper(strings.TrimSpace(result.CountryCode))
						g.UpdatedAt = now
						g.Valid = true
					}
				}
			}
		}
	}
	writeServerGeoCache(g)
	return geoDisplay(g)
}

func geoDisplay(g ServerGeo) (string, string) {
	if !g.Valid || strings.TrimSpace(g.IP) == "" || strings.TrimSpace(g.Country) == "" {
		return "Unavailable", "Unknown"
	}
	country := g.Country
	if g.CountryCode != "" {
		country += " (" + g.CountryCode + ")"
	}
	if !g.UpdatedAt.IsZero() && time.Since(g.UpdatedAt) > 6*time.Hour {
		country += " [cached]"
	}
	return g.IP, country
}

func menu() {
	if !ensureRoot() {
		return
	}
	for {
		state := paint(ansiYellow, "○ OFFLINE / NOT CONFIGURED")
		if tunnelIsActive() {
			state = paint(ansiGreen+ansiBold, "● ONLINE")
		}
		uiHeader("SHAH TUNNEL // COMMAND DECK", "SPACE NETWORK   •   ENCRYPTED CONTROL   •   TCP TUNNEL")
		fmt.Printf("  %s %s\n", paint(ansiDim, "VERSION"), paint(ansiCyan, version+"  |  "+runtimeArch()))
		fmt.Printf("  %s %s\n", paint(ansiDim, "LINK STATUS"), state)
		serverIP, serverCountry := serverIdentity()
		fmt.Printf("  %s %s\n", paint(ansiDim, "🌐 SERVER IP"), paint(ansiCyan, serverIP))
		fmt.Printf("  %s %s\n", paint(ansiDim, "🌍 COUNTRY"), paint(ansiCyan, serverCountry))
		uiRule()
		fmt.Println(paint(ansiCyan+ansiBold, "  OPERATIONS"))
		menuItem := func(key, title, desc string) {
			fmt.Printf("  %s  %s %s\n", paint(ansiPurple+ansiBold, "["+key+"]"), paint(ansiBold, fmt.Sprintf("%-32s", title)), paint(ansiDim, desc))
		}
		menuItem("1", "🛠️ Configure / Create Tunnel", "Step-by-step setup wizard")
		menuItem("2", "🚀 Start Tunnel", "Start the service")
		menuItem("3", "🛑 Stop Tunnel", "Stop the running service")
		menuItem("4", "🔄 Restart Tunnel", "Reload saved configuration")
		menuItem("5", "📊 Status & Logs", "Service status and recent errors")
		menuItem("6", "🗑️ Complete Uninstall", "Only Shah Tunnel files and service")
		menuItem("0", "🚪 Exit", "Close the menu")
		uiRule()
		choice := ask("Select an operation", "0")
		var err error
		switch choice {
		case "1":
			err = setup() // setup starts/restarts the service automatically on success
		case "2":
			err = ensureTunnelRunning(false)
		case "3":
			err = runSystemctl("stop", "ravanreverse")
			if err == nil && tunnelIsActive() {
				err = errors.New("service is still active after stop")
			}
		case "4":
			err = ensureTunnelRunning(true)
		case "5":
			err = showStatusAndLogs()
		case "6":
			err = uninstall()
			if err == nil {
				return
			}
			if errors.Is(err, errUninstallCancelled) {
				err = nil
			}
		case "0":
			return
		default:
			err = errors.New("Invalid option")
		}
		if err != nil {
			fmt.Printf("\n  %s %s\n", paint(ansiRed+ansiBold, "✗ ERROR:"), err)
		}
		if choice != "6" {
			_ = ask("Press Enter to return to the menu", "")
		}
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cmd := "menu"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "setup":
		if !ensureRoot() {
			os.Exit(1)
		}
		if err := setup(); err != nil {
			log.Fatal(err)
		}
	case "menu":
		menu()
	case "run":
		cfg, err := loadConfig()
		if err != nil {
			log.Fatal(err)
		}
		switch cfg.Role {
		case "hub":
			err = runHub(cfg)
		case "agent":
			err = runAgent(cfg)
		case "relay":
			err = runRelay(cfg)
		default:
			err = fmt.Errorf("unsupported role %q", cfg.Role)
		}
		if err != nil {
			log.Fatal(err)
		}
	case "uninstall":
		if !ensureRoot() {
			os.Exit(1)
		}
		if err := uninstall(); err != nil {
			log.Fatal(err)
		}
	case "version", "--version", "-v":
		fmt.Printf("Shah Tunnel %s (%s)\n", version, runtimeArch())
	default:
		fmt.Printf("Shah Tunnel %s\nUsage: shah-tunnel [menu|setup|run|uninstall|version]\n", version)
	}
}

func runtimeArch() string { return runtime.GOOS + "/" + runtime.GOARCH }
