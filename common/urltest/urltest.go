package urltest

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type History struct {
	Time  time.Time `json:"time"`
	Delay uint16    `json:"delay"`
}

type HistoryStorage struct {
	access       sync.RWMutex
	delayHistory map[string]*History
	updateHook   chan<- struct{}
}

func NewHistoryStorage() *HistoryStorage {
	return &HistoryStorage{
		delayHistory: make(map[string]*History),
	}
}

func (s *HistoryStorage) SetHook(hook chan<- struct{}) {
	s.updateHook = hook
}

func (s *HistoryStorage) LoadURLTestHistory(tag string) *History {
	if s == nil {
		return nil
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return s.delayHistory[tag]
}

func (s *HistoryStorage) DeleteURLTestHistory(tag string) {
	s.access.Lock()
	delete(s.delayHistory, tag)
	s.access.Unlock()
	s.notifyUpdated()
}

func (s *HistoryStorage) StoreURLTestHistory(tag string, history *History) {
	s.access.Lock()
	s.delayHistory[tag] = history
	s.access.Unlock()
	s.notifyUpdated()
}

func (s *HistoryStorage) notifyUpdated() {
	updateHook := s.updateHook
	if updateHook != nil {
		select {
		case updateHook <- struct{}{}:
		default:
		}
	}
}

func (s *HistoryStorage) Close() error {
	s.updateHook = nil
	return nil
}

func URLTest(ctx context.Context, link string, detour N.Dialer, log log.Logger, tag string) (t uint16, err error) {
	logger := func(args ...any) {
		if log != nil {
			log.Debug(args...)
		}
	}
	if link == "" {
		link = "https://www.gstatic.com/generate_204"
	}
	linkURL, err := url.Parse(link)
	if err != nil {
		return
	}
	hostname := linkURL.Hostname()
	port := linkURL.Port()
	if port == "" {
		switch linkURL.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}

	start := time.Now()
	instance, err := detour.DialContext(ctx, "tcp", M.ParseSocksaddrHostPortStr(hostname, port))
	if err != nil {
		return
	}
	defer instance.Close()
	if earlyConn, isEarlyConn := common.Cast[N.EarlyConn](instance); isEarlyConn && earlyConn.NeedHandshake() {
		start = time.Now()
	}
	req, err := http.NewRequest(http.MethodHead, link, nil)
	if err != nil {
		return
	}
	trace := &httptrace.ClientTrace{
		GotConn: func(connInfo httptrace.GotConnInfo) {
			logger("URLTest::httptrace Got Conn", "info", connInfo, "tag", tag)
		},
		DNSDone: func(dnsInfo httptrace.DNSDoneInfo) {
			logger("URLTest::httptrace DNS Info", "info", dnsInfo, "tag", tag)
		},
		ConnectDone: func(network, addr string, err error) {
			if err != nil {
				logger("URLTest::httptrace Connect Done", "network", network, "addr", addr, "tag", tag, "error", err)
			} else {
				logger("URLTest::httptrace Connect Done", "network", network, "addr", addr, "tag", tag)
			}
		},
		TLSHandshakeStart: func() {
			logger("URLTest::httptrace TLS Handshake Start", "tag", tag)
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			if err != nil {
				logger("URLTest::httptrace TLS Handshake Done", "tag", tag, "error", err)
			} else {
				logger("URLTest::httptrace TLS Handshake Done", "tag", tag)
			}
		},
		WroteHeaders: func() {
			logger("URLTest::httptrace Wrote Headers", "tag", tag)
		},
		GotFirstResponseByte: func() {
			logger("URLTest::httptrace Got First Response Byte", "tag", tag)
		},
	}
	req = req.WithContext(ctx)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	logger("Sending request with timeout", "timeout", C.TCPTimeout, "tag", tag)
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return instance, nil
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: C.TCPTimeout,
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	logger("URLTest::httptrace got full response", "status", resp.StatusCode, "tag", tag)
	resp.Body.Close()
	t = uint16(time.Since(start) / time.Millisecond)
	return
}
