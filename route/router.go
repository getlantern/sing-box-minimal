package route

import (
	"context"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/process"
	"github.com/sagernet/sing-box/common/taskmonitor"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/task"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/contrab/freelru"
	"github.com/sagernet/sing/contrab/maphash"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

var _ adapter.Router = (*Router)(nil)

type Router struct {
	ctx               context.Context
	logger            log.ContextLogger
	inbound           adapter.InboundManager
	outbound          adapter.OutboundManager
	dns               adapter.DNSRouter
	dnsTransport      adapter.DNSTransportManager
	connection        adapter.ConnectionManager
	network           adapter.NetworkManager
	httpClientManager adapter.HTTPClientManager
	rules             []adapter.Rule
	needFindProcess   bool
	needFindNeighbor  bool
	leaseFiles        []string
	ruleSets          []adapter.RuleSet
	ruleSetMap        map[string]adapter.RuleSet
	ruleSetUpdater    *R.RuleSetUpdater
	// processNeeded tracks whether any rule or rule-set needs process
	// lookups. Rule-sets can gain process rules on a live update, so the
	// searcher is built lazily by processInfoSearcher instead of once at start.
	processNeededMutex  sync.Mutex
	processNeeded       atomic.Bool
	processSearcherOnce sync.Once
	processSearcher     process.Searcher
	processCache        *freelru.Cache[processCacheKey, processCacheEntry]
	ruleSetCallbacks    []*list.Element[adapter.RuleSetUpdateCallback]
	neighborResolver    adapter.NeighborResolver
	pauseManager        pause.Manager
	trackers            []adapter.ConnectionTracker
	platformInterface   adapter.PlatformInterface
	started             bool
}

func NewRouter(ctx context.Context, logFactory log.Factory, options option.RouteOptions, dnsOptions option.DNSOptions) *Router {
	return &Router{
		ctx:               ctx,
		logger:            logFactory.NewLogger("router"),
		inbound:           service.FromContext[adapter.InboundManager](ctx),
		outbound:          service.FromContext[adapter.OutboundManager](ctx),
		dns:               service.FromContext[adapter.DNSRouter](ctx),
		dnsTransport:      service.FromContext[adapter.DNSTransportManager](ctx),
		connection:        service.FromContext[adapter.ConnectionManager](ctx),
		network:           service.FromContext[adapter.NetworkManager](ctx),
		httpClientManager: service.FromContext[adapter.HTTPClientManager](ctx),
		rules:             make([]adapter.Rule, 0, len(options.Rules)),
		ruleSetMap:        make(map[string]adapter.RuleSet),
		needFindProcess:   hasRule(options.Rules, isProcessRule) || hasDNSRule(dnsOptions.Rules, isProcessDNSRule) || options.FindProcess,
		needFindNeighbor:  hasRule(options.Rules, isNeighborRule) || hasDNSRule(dnsOptions.Rules, isNeighborDNSRule) || hasLocalNeighborDNSServer(dnsOptions.Servers) || options.FindNeighbor,
		leaseFiles:        options.DHCPLeaseFiles,
		pauseManager:      service.FromContext[pause.Manager](ctx),
		platformInterface: service.FromContext[adapter.PlatformInterface](ctx),
	}
}

func (r *Router) Initialize(rules []option.Rule, ruleSets []option.RuleSet) error {
	for i, options := range rules {
		err := R.ValidateNoNestedRuleActions(options)
		if err != nil {
			return E.Cause(err, "parse rule[", i, "]")
		}
		rule, err := R.NewRule(r.ctx, r.logger, options, false)
		if err != nil {
			return E.Cause(err, "parse rule[", i, "]")
		}
		r.rules = append(r.rules, rule)
	}
	for i, options := range ruleSets {
		for _, tag := range options.Tag {
			if _, exists := r.ruleSetMap[tag]; exists {
				return E.New("duplicate rule-set tag: ", tag)
			}
			ruleSet, err := R.NewRuleSet(r.ctx, r.logger, tag, options)
			if err != nil {
				return E.Cause(err, "parse rule-set[", i, "]")
			}
			r.ruleSets = append(r.ruleSets, ruleSet)
			r.ruleSetMap[tag] = ruleSet
		}
	}
	return nil
}

func (r *Router) Start(stage adapter.StartStage) error {
	monitor := taskmonitor.New(r.logger, C.StartTimeout)
	switch stage {
	case adapter.StartStateInitialize:
		if r.needFindNeighbor {
			if r.platformInterface != nil && r.platformInterface.UsePlatformNeighborResolver() {
				monitor.Start("initialize neighbor resolver")
				resolver := newPlatformNeighborResolver(r.logger, r.platformInterface)
				err := resolver.Start()
				monitor.Finish()
				if err != nil {
					r.logger.Error(E.Cause(err, "start neighbor resolver"))
				} else {
					r.neighborResolver = resolver
				}
			} else {
				monitor.Start("initialize neighbor resolver")
				resolver, err := newNeighborResolver(r.logger, r.leaseFiles)
				monitor.Finish()
				if err != nil {
					if err != os.ErrInvalid {
						r.logger.Error(E.Cause(err, "create neighbor resolver"))
					}
				} else {
					err = resolver.Start()
					if err != nil {
						r.logger.Error(E.Cause(err, "start neighbor resolver"))
					} else {
						r.neighborResolver = resolver
					}
				}
			}
		}
	case adapter.StartStateStart:
		var startContext *adapter.HTTPStartContext
		if len(r.ruleSets) > 0 {
			monitor.Start("initialize rule-set")
			startContext = adapter.NewHTTPStartContext()
			var ruleSetStartGroup task.Group
			for i, ruleSet := range r.ruleSets {
				ruleSetInPlace := ruleSet
				ruleSetStartGroup.Append0(func(ctx context.Context) error {
					err := ruleSetInPlace.StartContext(ctx, startContext)
					if err != nil {
						return E.Cause(err, "initialize rule-set[", i, "]")
					}
					return nil
				})
			}
			ruleSetStartGroup.Concurrency(5)
			ruleSetStartGroup.FastFail()
			err := ruleSetStartGroup.Run(r.ctx)
			monitor.Finish()
			if err != nil {
				return err
			}
		}
		if startContext != nil {
			startContext.Close()
		}
		r.ruleSetUpdater = R.NewRuleSetUpdater(r.ctx, r.ruleSets)
		r.network.Initialize(r.ruleSets)
		for _, ruleSet := range r.ruleSets {
			r.ruleSetCallbacks = append(r.ruleSetCallbacks, ruleSet.RegisterCallback(func(adapter.RuleSet) {
				r.updateProcessNeeded()
			}))
		}
		r.updateProcessNeeded()
		if r.processNeeded.Load() {
			monitor.Start("initialize process searcher")
			r.processInfoSearcher()
			monitor.Finish()
		}
	case adapter.StartStatePostStart:
		for i, rule := range r.rules {
			monitor.Start("initialize rule[", i, "]")
			err := rule.Start()
			monitor.Finish()
			if err != nil {
				return E.Cause(err, "initialize rule[", i, "]")
			}
		}
		if r.ruleSetUpdater != nil {
			r.ruleSetUpdater.Start()
		}
		r.started = true
		return nil
	case adapter.StartStateStarted:
		for _, ruleSet := range r.ruleSets {
			ruleSet.Cleanup()
		}
		runtime.GC()
	}
	return nil
}

func (r *Router) Close() error {
	monitor := taskmonitor.New(r.logger, C.StopTimeout)
	var err error
	if r.neighborResolver != nil {
		monitor.Start("close neighbor resolver")
		err = E.Append(err, r.neighborResolver.Close(), func(closeErr error) error {
			return E.Cause(closeErr, "close neighbor resolver")
		})
		monitor.Finish()
	}
	for i, rule := range r.rules {
		monitor.Start("close rule[", i, "]")
		err = E.Append(err, rule.Close(), func(err error) error {
			return E.Cause(err, "close rule[", i, "]")
		})
		monitor.Finish()
	}
	if r.ruleSetUpdater != nil {
		monitor.Start("close rule-set updater")
		err = E.Append(err, r.ruleSetUpdater.Close(), func(err error) error {
			return E.Cause(err, "close rule-set updater")
		})
		monitor.Finish()
	}
	for i, ruleSet := range r.ruleSets {
		if i < len(r.ruleSetCallbacks) {
			ruleSet.UnregisterCallback(r.ruleSetCallbacks[i])
		}
		monitor.Start("close rule-set[", i, "]")
		err = E.Append(err, ruleSet.Close(), func(err error) error {
			return E.Cause(err, "close rule-set[", i, "]")
		})
		monitor.Finish()
	}
	r.processNeededMutex.Lock()
	processSearcher := r.processSearcher
	r.processNeededMutex.Unlock()
	if processSearcher != nil {
		monitor.Start("close process searcher")
		err = E.Append(err, processSearcher.Close(), func(err error) error {
			return E.Cause(err, "close process searcher")
		})
		monitor.Finish()
	}
	return err
}

func (r *Router) updateProcessNeeded() {
	r.processNeededMutex.Lock()
	defer r.processNeededMutex.Unlock()
	need := r.needFindProcess
	if !need {
		for _, ruleSet := range r.ruleSets {
			if ruleSet.Metadata().ContainsProcessRule {
				need = true
				break
			}
		}
	}
	if C.IsAndroid && r.platformInterface != nil {
		need = true
	}
	r.processNeeded.Store(need)
}

// processInfoSearcher returns the process searcher, building it on first use
// once a rule needs it. Rule-set update callbacks only flip processNeeded, so
// construction never runs on a rule-set update goroutine.
func (r *Router) processInfoSearcher() process.Searcher {
	if !r.processNeeded.Load() {
		return nil
	}
	r.processSearcherOnce.Do(func() {
		var searcher process.Searcher
		if r.platformInterface != nil && r.platformInterface.UsePlatformConnectionOwnerFinder() {
			searcher = newPlatformSearcher(r.platformInterface)
		} else {
			s, err := process.NewSearcher(process.Config{
				Logger:         r.logger,
				PackageManager: r.network.PackageManager(),
			})
			if err != nil {
				if err != os.ErrInvalid {
					r.logger.Warn(E.Cause(err, "create process searcher"))
				}
				return
			}
			searcher = s
		}
		processCache := common.Must1(freelru.New[processCacheKey, processCacheEntry](256, maphash.NewHasher[processCacheKey]().Hash32, true))
		processCache.SetLifetime(200 * time.Millisecond)
		r.processNeededMutex.Lock()
		r.processSearcher = searcher
		r.processCache = processCache
		r.processNeededMutex.Unlock()
	})
	return r.processSearcher
}

func (r *Router) RuleSet(tag string) (adapter.RuleSet, bool) {
	ruleSet, loaded := r.ruleSetMap[tag]
	return ruleSet, loaded
}

func (r *Router) Rules() []adapter.Rule {
	return r.rules
}

func (r *Router) AppendTracker(tracker adapter.ConnectionTracker) {
	r.trackers = append(r.trackers, tracker)
}

func (r *Router) NeedFindProcess() bool {
	return r.processNeeded.Load()
}

func (r *Router) NeedFindNeighbor() bool {
	return r.needFindNeighbor
}

func (r *Router) NeighborResolver() adapter.NeighborResolver {
	return r.neighborResolver
}

func (r *Router) ResetNetwork() {
	r.httpClientManager.ResetNetwork()
	r.dns.ResetNetwork()
	r.processNeededMutex.Lock()
	processCache := r.processCache
	processSearcher := r.processSearcher
	r.processNeededMutex.Unlock()
	if processCache != nil {
		processCache.Purge()
	}
	if processSearcher != nil {
		processSearcher.ResetCache()
	}
}
