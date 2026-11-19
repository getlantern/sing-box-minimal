//go:build with_utls

package tls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	utls "github.com/metacubex/utls"
)

const (
	echTestPublicName = "public.example"
	echTestInnerName  = "inner.example"
	echTestOuterName  = "override.example"
)

type helloRecordingDialer struct {
	mu     sync.Mutex
	hellos [][]byte
}

func (d *helloRecordingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, destination.String())
	if err != nil {
		return nil, err
	}
	return &helloRecordingConn{Conn: conn, dialer: d}, nil
}

func (d *helloRecordingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	panic("unused")
}

// outerSNIs returns the SNI of each dial's first ClientHello.
func (d *helloRecordingDialer) outerSNIs(t *testing.T) []string {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	var names []string
	for _, raw := range d.hellos {
		n := int(raw[3])<<8 | int(raw[4])
		hello := utls.UnmarshalClientHello(raw[5 : 5+n])
		if hello == nil {
			t.Fatal("cannot parse ClientHello")
		}
		names = append(names, hello.ServerName)
	}
	return names
}

type helloRecordingConn struct {
	net.Conn
	dialer  *helloRecordingDialer
	written bool
}

func (c *helloRecordingConn) Write(p []byte) (int, error) {
	if !c.written {
		c.written = true
		c.dialer.mu.Lock()
		c.dialer.hellos = append(c.dialer.hellos, append([]byte(nil), p...))
		c.dialer.mu.Unlock()
	}
	return c.Conn.Write(p)
}

var _ N.Dialer = (*helloRecordingDialer)(nil)

func newECHTestKey(t *testing.T) (configPEM string, key tls.EncryptedClientHelloKey) {
	t.Helper()
	configPEM, keyPEM, err := ECHKeygenDefault(echTestPublicName)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := parseECHKeys([]byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	keys[0].SendAsRetry = true
	return configPEM, keys[0]
}

func newECHTestCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: echTestInnerName},
		DNSNames:              []string{echTestInnerName, echTestOuterName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// startECHServer accepts one handshake per connection and reports the
// server-side view of each.
func startECHServer(t *testing.T, cert tls.Certificate, key tls.EncryptedClientHelloKey) (string, <-chan tls.ConnectionState) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates:             []tls.Certificate{cert},
		EncryptedClientHelloKeys: []tls.EncryptedClientHelloKey{key},
		NextProtos:               []string{"h2", "http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	states := make(chan tls.ConnectionState, 64)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				tc := conn.(*tls.Conn)
				if tc.Handshake() == nil {
					states <- tc.ConnectionState()
					_, _ = tc.Write([]byte{1})
				}
			}()
		}
	}()
	return ln.Addr().String(), states
}

// newECHOuterConfig uses a Chrome fingerprint; alpn nil keeps the fingerprint's own ALPN list.
func newECHOuterConfig(t *testing.T, configPEM, rootPEM string, alpn []string) Config {
	t.Helper()
	config, err := NewUTLSClient(context.Background(), logger.NOP(), "", option.OutboundTLSOptions{
		Enabled:     true,
		ServerName:  echTestInnerName,
		Certificate: []string{rootPEM},
		ALPN:        alpn,
		UTLS:        &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"},
		ECH: &option.OutboundECHOptions{
			Enabled:         true,
			Config:          []string{configPEM},
			OuterServerName: echTestOuterName,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func dialAndRead(dialer Dialer, addr string) (Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialer.DialTLSContext(ctx, M.ParseSocksaddr(addr))
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	if _, err := conn.Read(make([]byte, 1)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("read: %w", err)
	}
	return conn, nil
}

func dialECHOuter(t *testing.T, addr, configPEM, rootPEM string, alpn []string) (Conn, *helloRecordingDialer) {
	t.Helper()
	recorder := &helloRecordingDialer{}
	conn, err := dialAndRead(NewDialer(recorder, newECHOuterConfig(t, configPEM, rootPEM, alpn)), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, recorder
}

func TestECHOuterServerName(t *testing.T) {
	cert, rootPEM := newECHTestCert(t)
	configPEM, key := newECHTestKey(t)
	addr, states := startECHServer(t, cert, key)

	conn, recorder := dialECHOuter(t, addr, configPEM, rootPEM, []string{"http/1.1"})
	if !conn.ConnectionState().ECHAccepted {
		t.Error("client: ECH not accepted")
	}
	if got := recorder.outerSNIs(t); len(got) != 1 || got[0] != echTestOuterName {
		t.Errorf("outer SNIs = %q, want [%q]", got, echTestOuterName)
	}
	server := <-states
	if !server.ECHAccepted || server.ServerName != echTestInnerName {
		t.Errorf("server: ECHAccepted=%v ServerName=%q", server.ECHAccepted, server.ServerName)
	}
}

func TestECHOuterServerNameStaleConfigRetries(t *testing.T) {
	for name, alpn := range map[string][]string{"explicit-alpn": {"http/1.1"}, "fingerprint-alpn": nil} {
		t.Run(name, func(t *testing.T) {
			cert, rootPEM := newECHTestCert(t)
			_, key := newECHTestKey(t)
			staleConfigPEM, _ := newECHTestKey(t)
			addr, states := startECHServer(t, cert, key)

			conn, recorder := dialECHOuter(t, addr, staleConfigPEM, rootPEM, alpn)
			if !conn.ConnectionState().ECHAccepted {
				t.Error("client: ECH not accepted after retry")
			}
			got := recorder.outerSNIs(t)
			if len(got) != 2 || got[0] != echTestOuterName || got[1] != echTestOuterName {
				t.Errorf("outer SNIs = %q, want two dials with %q", got, echTestOuterName)
			}
			rejected, accepted := <-states, <-states
			if rejected.ECHAccepted {
				rejected, accepted = accepted, rejected
			}
			if rejected.ECHAccepted || rejected.ServerName != echTestOuterName {
				t.Errorf("first handshake: ECHAccepted=%v ServerName=%q, want a rejection for %q",
					rejected.ECHAccepted, rejected.ServerName, echTestOuterName)
			}
			if !accepted.ECHAccepted || accepted.ServerName != echTestInnerName {
				t.Errorf("retry: ECHAccepted=%v ServerName=%q", accepted.ECHAccepted, accepted.ServerName)
			}
		})
	}
}

// TestECHOuterServerNameConcurrentRetries rotates the key under dials that
// share one config, as when the provider rotates keys under live traffic.
func TestECHOuterServerNameConcurrentRetries(t *testing.T) {
	cert, rootPEM := newECHTestCert(t)
	_, key := newECHTestKey(t)
	staleConfigPEM, _ := newECHTestKey(t)
	addr, _ := startECHServer(t, cert, key)
	dialer := NewDialer(&helloRecordingDialer{}, newECHOuterConfig(t, staleConfigPEM, rootPEM, []string{"http/1.1"}))

	const dials = 8
	errs := make(chan error, dials)
	for range dials {
		go func() {
			conn, err := dialAndRead(dialer, addr)
			if err == nil {
				if !conn.ConnectionState().ECHAccepted {
					err = errors.New("ECH not accepted")
				}
				conn.Close()
			}
			errs <- err
		}()
	}
	for range dials {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

func TestECHOuterServerNameRequiresUTLS(t *testing.T) {
	_, err := NewSTDClient(context.Background(), logger.NOP(), "", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: echTestInnerName,
		ECH:        &option.OutboundECHOptions{Enabled: true, OuterServerName: echTestOuterName},
	})
	if err == nil {
		t.Fatal("std client accepted ech.outer_server_name")
	}
}
