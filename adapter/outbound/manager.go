package outbound

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/taskmonitor"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
)

var _ adapter.OutboundManager = (*Manager)(nil)

type Manager struct {
	logger                  log.ContextLogger
	registry                adapter.OutboundRegistry
	endpoint                adapter.EndpointManager
	defaultTag              string
	access                  sync.RWMutex
	started                 bool
	stage                   adapter.StartStage
	outbounds               []adapter.Outbound
	outboundByTag           map[string]adapter.Outbound
	dependByTag             map[string][]string
	defaultOutbound         adapter.Outbound
	defaultOutboundFallback func() (adapter.Outbound, error)
}

func NewManager(logger logger.ContextLogger, registry adapter.OutboundRegistry, endpoint adapter.EndpointManager, defaultTag string) *Manager {
	return &Manager{
		logger:        logger,
		registry:      registry,
		endpoint:      endpoint,
		defaultTag:    defaultTag,
		outboundByTag: make(map[string]adapter.Outbound),
		dependByTag:   make(map[string][]string),
	}
}

func (m *Manager) Initialize(defaultOutboundFallback func() (adapter.Outbound, error)) {
	m.defaultOutboundFallback = defaultOutboundFallback
}

func (m *Manager) Start(stage adapter.StartStage) error {
	m.access.Lock()
	if m.started && m.stage >= stage {
		panic("already started")
	}
	m.started = true
	m.stage = stage
	if stage == adapter.StartStateStart {
		if m.defaultTag != "" && m.defaultOutbound == nil {
			defaultEndpoint, loaded := m.endpoint.Get(m.defaultTag)
			if !loaded {
				m.access.Unlock()
				return E.New("default outbound not found: ", m.defaultTag)
			}
			m.defaultOutbound = defaultEndpoint
		}
		if m.defaultOutbound == nil {
			directOutbound, err := m.defaultOutboundFallback()
			if err != nil {
				m.access.Unlock()
				return E.Cause(err, "create direct outbound for fallback")
			}
			m.outbounds = append(m.outbounds, directOutbound)
			m.outboundByTag[directOutbound.Tag()] = directOutbound
			m.defaultOutbound = directOutbound
		}
		outbounds := m.outbounds
		m.access.Unlock()
		return m.startOutbounds(append(outbounds, common.Map(m.endpoint.Endpoints(), func(it adapter.Endpoint) adapter.Outbound { return it })...))
	} else {
		outbounds := m.outbounds
		m.access.Unlock()
		for _, outbound := range outbounds {
			err := adapter.LegacyStart(outbound, stage)
			if err != nil {
				return E.Cause(err, stage, " outbound/", outbound.Type(), "[", outbound.Tag(), "]")
			}
		}
	}
	return nil
}

func (m *Manager) startOutbounds(outbounds []adapter.Outbound) error {
	monitor := taskmonitor.New(m.logger, C.StartTimeout)
	started := make(map[string]bool)
	for {
		canContinue := false
	startOne:
		for _, outboundToStart := range outbounds {
			outboundTag := outboundToStart.Tag()
			if started[outboundTag] {
				continue
			}
			dependencies := outboundToStart.Dependencies()
			for _, dependency := range dependencies {
				if !started[dependency] {
					continue startOne
				}
			}
			started[outboundTag] = true
			canContinue = true
			if starter, isStarter := outboundToStart.(adapter.Lifecycle); isStarter {
				monitor.Start("start outbound/", outboundToStart.Type(), "[", outboundTag, "]")
				err := starter.Start(adapter.StartStateStart)
				monitor.Finish()
				if err != nil {
					return E.Cause(err, "start outbound/", outboundToStart.Type(), "[", outboundTag, "]")
				}
			} else if starter, isStarter := outboundToStart.(interface {
				Start() error
			}); isStarter {
				monitor.Start("start outbound/", outboundToStart.Type(), "[", outboundTag, "]")
				err := starter.Start()
				monitor.Finish()
				if err != nil {
					return E.Cause(err, "start outbound/", outboundToStart.Type(), "[", outboundTag, "]")
				}
			}
		}
		if len(started) == len(outbounds) {
			break
		}
		if canContinue {
			continue
		}
		currentOutbound := common.Find(outbounds, func(it adapter.Outbound) bool {
			return !started[it.Tag()]
		})
		var lintOutbound func(oTree []string, oCurrent adapter.Outbound) error
		lintOutbound = func(oTree []string, oCurrent adapter.Outbound) error {
			problemOutboundTag := common.Find(oCurrent.Dependencies(), func(it string) bool {
				return !started[it]
			})
			if common.Contains(oTree, problemOutboundTag) {
				return E.New("circular outbound dependency: ", strings.Join(oTree, " -> "), " -> ", problemOutboundTag)
			}
			m.access.Lock()
			problemOutbound := m.outboundByTag[problemOutboundTag]
			m.access.Unlock()
			if problemOutbound == nil {
				return E.New("dependency[", problemOutboundTag, "] not found for outbound[", oCurrent.Tag(), "]")
			}
			return lintOutbound(append(oTree, problemOutboundTag), problemOutbound)
		}
		return lintOutbound([]string{currentOutbound.Tag()}, currentOutbound)
	}
	return nil
}

func (m *Manager) Close() error {
	monitor := taskmonitor.New(m.logger, C.StopTimeout)
	m.access.Lock()
	if !m.started {
		m.access.Unlock()
		return nil
	}
	m.started = false
	outbounds := m.outbounds
	m.outbounds = nil
	// Keep the map in sync with the slice: leaving stale entries behind is
	// what made Remove racing Close find a tag with no slice entry. The
	// dependency bookkeeping goes with them. defaultOutbound is deliberately
	// left alone: route dispatch dereferences Default() without a nil check
	// (route/route.go), so in-flight connections racing shutdown must keep
	// getting the stale-but-closed outbound (which fails at dial) rather
	// than a nil (which would panic).
	clear(m.outboundByTag)
	clear(m.dependByTag)
	m.access.Unlock()
	var err error
	for _, outbound := range outbounds {
		if closer, isCloser := outbound.(io.Closer); isCloser {
			monitor.Start("close outbound/", outbound.Type(), "[", outbound.Tag(), "]")
			err = E.Append(err, closer.Close(), func(err error) error {
				return E.Cause(err, "close outbound/", outbound.Type(), "[", outbound.Tag(), "]")
			})
			monitor.Finish()
		}
	}
	return nil
}

func (m *Manager) Outbounds() []adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.outbounds
}

func (m *Manager) Outbound(tag string) (adapter.Outbound, bool) {
	m.access.RLock()
	outbound, found := m.outboundByTag[tag]
	m.access.RUnlock()
	if found {
		return outbound, true
	}
	return m.endpoint.Get(tag)
}

func (m *Manager) Default() adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.defaultOutbound
}

func (m *Manager) Remove(tag string) error {
	m.access.Lock()
	defer m.access.Unlock()
	outbound, found := m.outboundByTag[tag]
	if !found {
		return os.ErrInvalid
	}
	index := common.Index(m.outbounds, func(it adapter.Outbound) bool {
		return it == outbound
	})
	delete(m.outboundByTag, tag)
	if index == -1 {
		// Defense in depth: the tag is in the map but not the slice. No code
		// path should produce this state (Close clears both together), but if
		// it ever reappears, heal it instead of panicking — skip the slice
		// splice and fall through so the rest of the bookkeeping (default
		// outbound, dependency entries, close) still runs.
		m.logger.Debug("outbound/", outbound.Type(), "[", tag, "] already removed from active list")
	} else {
		m.outbounds = append(m.outbounds[:index], m.outbounds[index+1:]...)
	}
	started := m.started
	if m.defaultOutbound == outbound {
		if len(m.outbounds) > 0 {
			m.defaultOutbound = m.outbounds[0]
			m.logger.Info("updated default outbound to ", m.defaultOutbound.Tag())
		} else {
			m.defaultOutbound = nil
		}
	}
	dependBy := m.dependByTag[tag]
	if len(dependBy) > 0 {
		return E.New("outbound[", tag, "] is depended by ", strings.Join(dependBy, ", "))
	}
	m.removeDependByEntriesLocked(outbound, tag)
	if started {
		return common.Close(outbound)
	}
	return nil
}

// removeDependByEntriesLocked drops tag from the dependBy lists of outbound's
// dependencies. Callers must hold m.access.
func (m *Manager) removeDependByEntriesLocked(outbound adapter.Outbound, tag string) {
	for _, dependency := range outbound.Dependencies() {
		if len(m.dependByTag[dependency]) == 1 {
			delete(m.dependByTag, dependency)
		} else {
			m.dependByTag[dependency] = common.Filter(m.dependByTag[dependency], func(it string) bool {
				return it != tag
			})
		}
	}
}

func (m *Manager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, inboundType string, options any) error {
	if tag == "" {
		return os.ErrInvalid
	}
	outbound, err := m.registry.CreateOutbound(ctx, router, logger, tag, inboundType, options)
	if err != nil {
		return err
	}
	// The replacement starts before the predecessor closes (make before break), so
	// a replacement that fails to start leaves the existing outbound serving this
	// tag rather than dropping it. The cost is a transient window where both are
	// live, which is why teardown has to be prompt.
	if m.started {
		for _, stage := range adapter.ListStartStages {
			err = adapter.LegacyStart(outbound, stage)
			if err != nil {
				// This outbound never reaches m.outbounds/outboundByTag on the error
				// path, so nothing else can ever close it. Release whatever the
				// earlier stages brought up before returning, or it leaks for the
				// lifetime of the process.
				if closeErr := common.Close(outbound); closeErr != nil {
					m.logger.Warn("close partially started outbound/", outbound.Type(), "[", outbound.Tag(), "]: ", closeErr)
				}
				return E.Cause(err, stage, " outbound/", outbound.Type(), "[", outbound.Tag(), "]")
			}
		}
	}
	m.access.Lock()
	defer m.access.Unlock()
	if existsOutbound, loaded := m.outboundByTag[tag]; loaded {
		if m.started {
			// Log rather than abort. The predecessor is being discarded either way,
			// and returning here would leave it registered-but-closed while the
			// replacement — already fully started — never gets registered, and so
			// could never be closed either.
			if closeErr := common.Close(existsOutbound); closeErr != nil {
				m.logger.Warn("close outbound/", existsOutbound.Type(), "[", existsOutbound.Tag(), "]: ", closeErr)
			}
		}
		existsIndex := common.Index(m.outbounds, func(it adapter.Outbound) bool {
			return it == existsOutbound
		})
		if existsIndex != -1 {
			m.outbounds = append(m.outbounds[:existsIndex], m.outbounds[existsIndex+1:]...)
		}
		// The replacement may declare different dependencies; drop the old
		// outbound's dependBy entries so stale ones can't block Remove later.
		m.removeDependByEntriesLocked(existsOutbound, tag)
	}
	m.outbounds = append(m.outbounds, outbound)
	m.outboundByTag[tag] = outbound
	dependencies := outbound.Dependencies()
	for _, dependency := range dependencies {
		m.dependByTag[dependency] = append(m.dependByTag[dependency], tag)
	}
	if tag == m.defaultTag || (m.defaultTag == "" && m.defaultOutbound == nil) {
		m.defaultOutbound = outbound
		if m.started {
			m.logger.Info("updated default outbound to ", outbound.Tag())
		}
	}
	return nil
}
