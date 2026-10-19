package rule

import (
	"context"
	"runtime"
	"time"

	"github.com/sagernet/sing-box/adapter"
)

// A rule-set that has never loaded retries on a backoff from
// ruleSetRetryMin up to ruleSetRetryMax, rather than waiting a full update
// interval: its initial fetch may have failed only because the network was
// not up yet.
var (
	ruleSetRetryMin = 30 * time.Second
	ruleSetRetryMax = 10 * time.Minute
)

type RuleSetUpdater struct {
	ctx      context.Context
	cancel   context.CancelFunc
	ruleSets []*RemoteRuleSet
}

func NewRuleSetUpdater(ctx context.Context, ruleSets []adapter.RuleSet) *RuleSetUpdater {
	var remoteRuleSets []*RemoteRuleSet
	for _, ruleSet := range ruleSets {
		remoteRuleSet, isRemote := ruleSet.(*RemoteRuleSet)
		if isRemote {
			remoteRuleSets = append(remoteRuleSets, remoteRuleSet)
		}
	}
	if len(remoteRuleSets) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	return &RuleSetUpdater{
		ctx:      ctx,
		cancel:   cancel,
		ruleSets: remoteRuleSets,
	}
}

func (u *RuleSetUpdater) Start() {
	go u.loopUpdate()
}

func (u *RuleSetUpdater) Close() error {
	u.cancel()
	return nil
}

func (u *RuleSetUpdater) loopUpdate() {
	nextUpdates := make([]time.Time, len(u.ruleSets))
	retryDelays := make([]time.Duration, len(u.ruleSets))
	for i, ruleSet := range u.ruleSets {
		nextUpdates[i] = ruleSet.lastUpdated.Add(ruleSet.updateInterval)
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now()
		var updated bool
		for i, ruleSet := range u.ruleSets {
			if now.Before(nextUpdates[i]) {
				continue
			}
			ruleSet.updateOnce()
			if ruleSet.lastUpdated.IsZero() {
				retryDelays[i] = nextRetryDelay(retryDelays[i], ruleSet.updateInterval)
				nextUpdates[i] = now.Add(retryDelays[i])
			} else {
				retryDelays[i] = 0
				nextUpdates[i] = now.Add(ruleSet.updateInterval)
			}
			updated = true
		}
		if updated {
			runtime.GC()
		}
		timer.Reset(waitUntilNext(nextUpdates))
	}
}

func nextRetryDelay(previous time.Duration, updateInterval time.Duration) time.Duration {
	delay := ruleSetRetryMin
	if previous > 0 {
		delay = previous * 2
	}
	return min(delay, ruleSetRetryMax, updateInterval)
}

func waitUntilNext(nextUpdates []time.Time) time.Duration {
	next := nextUpdates[0]
	for _, nextUpdate := range nextUpdates[1:] {
		if nextUpdate.Before(next) {
			next = nextUpdate
		}
	}
	wait := time.Until(next)
	if wait < 0 {
		return 0
	}
	return wait
}
