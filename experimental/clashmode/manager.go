package clashmode

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/observable"
	"github.com/sagernet/sing/service"
)

type Manager struct {
	ctx          context.Context
	logger       log.Logger
	dnsRouter    adapter.DNSRouter
	mode         atomic.Pointer[string]
	modeList     []string
	updateAccess sync.Mutex
	updateHooks  []*observable.Subscriber[struct{}]
}

func NewManager(ctx context.Context, logger log.Logger, defaultMode string, modeList []string) *Manager {
	if defaultMode == "" {
		defaultMode = "Rule"
	}
	if !common.Contains(modeList, defaultMode) {
		modeList = append([]string{defaultMode}, modeList...)
	}
	manager := &Manager{
		ctx:       ctx,
		logger:    logger,
		dnsRouter: service.FromContext[adapter.DNSRouter](ctx),
		modeList:  modeList,
	}
	manager.mode.Store(&defaultMode)
	return manager
}

func (m *Manager) Name() string {
	return "clash mode manager"
}

func (m *Manager) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	cacheFile := service.FromContext[adapter.CacheFile](m.ctx)
	if cacheFile != nil {
		mode := cacheFile.LoadMode()
		if common.Any(m.modeList, func(it string) bool {
			return strings.EqualFold(it, mode)
		}) {
			m.mode.Store(&mode)
		}
	}
	return nil
}

func (m *Manager) Close() error {
	return nil
}

func (m *Manager) Mode() string {
	return *m.mode.Load()
}

func (m *Manager) ModeList() []string {
	return m.modeList
}

func (m *Manager) AddUpdateHook(hook *observable.Subscriber[struct{}]) {
	m.updateAccess.Lock()
	defer m.updateAccess.Unlock()
	m.updateHooks = append(m.updateHooks, hook)
}

func (m *Manager) SetMode(newMode string) {
	if !common.Contains(m.modeList, newMode) {
		newMode = common.Find(m.modeList, func(it string) bool {
			return strings.EqualFold(it, newMode)
		})
	}
	if !common.Contains(m.modeList, newMode) {
		return
	}
	if *m.mode.Swap(&newMode) == newMode {
		return
	}
	m.updateAccess.Lock()
	for _, hook := range m.updateHooks {
		hook.Emit(struct{}{})
	}
	m.updateAccess.Unlock()
	m.dnsRouter.ClearCache()
	cacheFile := service.FromContext[adapter.CacheFile](m.ctx)
	if cacheFile != nil {
		err := cacheFile.StoreMode(newMode)
		if err != nil {
			m.logger.Error(E.Cause(err, "save mode"))
		}
	}
	m.logger.Info("updated mode: ", newMode)
}

var _ adapter.LifecycleService = (*Manager)(nil)
