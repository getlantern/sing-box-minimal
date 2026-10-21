package adapter

import (
	"context"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/common/processpath"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

type Inbound interface {
	Lifecycle
	Type() string
	Tag() string
}

type TCPInjectableInbound interface {
	Inbound
	ConnectionHandlerEx
}

type UDPInjectableInbound interface {
	Inbound
	PacketConnectionHandlerEx
}

type InboundRegistry interface {
	option.InboundOptionsRegistry
	Registry
	Create(ctx context.Context, router Router, logger log.ContextLogger, tag string, inboundType string, options any) (Inbound, error)
}

type InboundManager interface {
	Lifecycle
	Inbounds() []Inbound
	Get(tag string) (Inbound, bool)
	Remove(tag string) error
	Create(ctx context.Context, router Router, logger log.ContextLogger, tag string, inboundType string, options any) error
}

type InboundContext struct {
	Inbound     string
	InboundType string
	IPVersion   uint8
	Network     string
	Source      M.Socksaddr
	Destination M.Socksaddr
	User        string
	Outbound    string

	// sniffer

	Protocol     string
	Domain       string
	Client       string
	SniffContext any
	SnifferNames []string
	SniffError   error

	// cache

	// Deprecated: implement in rule action
	InboundDetour             string
	LastInbound               string
	OriginDestination         M.Socksaddr
	RouteOriginalDestination  M.Socksaddr
	UDPDisableDomainUnmapping bool
	UDPConnect                bool
	UDPTimeout                time.Duration
	TLSFragment               bool
	TLSFragmentFallbackDelay  time.Duration
	TLSRecordFragment         bool

	NetworkStrategy     *C.NetworkStrategy
	NetworkType         []C.InterfaceType
	FallbackNetworkType []C.InterfaceType
	FallbackDelay       time.Duration

	DestinationAddresses []netip.Addr
	SourceGeoIPCode      string
	GeoIPCode            string
	ProcessInfo          *ConnectionOwner
	QueryType            uint16
	FakeIP               bool

	processPathSource     string
	normalizedProcessPath string

	// rule cache

	IPCIDRMatchSource bool
	IPCIDRAcceptEmpty bool

	SourceAddressMatch           bool
	SourcePortMatch              bool
	DestinationAddressMatch      bool
	DestinationPortMatch         bool
	DidMatch                     bool
	IgnoreDestinationIPCIDRMatch bool
}

// NormalizedProcessPath caches the path identity across rule evaluations without
// changing ProcessInfo, which can be shared with other connections.
func (c *InboundContext) NormalizedProcessPath() string {
	if c.ProcessInfo == nil {
		return ""
	}
	path := c.ProcessInfo.ProcessPath
	if path != c.processPathSource {
		c.processPathSource = path
		c.normalizedProcessPath = processpath.Normalize(path)
	}
	return c.normalizedProcessPath
}

// CopyProcessPathCacheFrom retains a nested rule's cached identity without
// copying its match state or a cache for a different process path.
func (c *InboundContext) CopyProcessPathCacheFrom(other *InboundContext) {
	if c.ProcessInfo != nil && c.ProcessInfo.ProcessPath == other.processPathSource {
		c.processPathSource = other.processPathSource
		c.normalizedProcessPath = other.normalizedProcessPath
	}
}

func (c *InboundContext) ResetRuleCache() {
	c.IPCIDRMatchSource = false
	c.IPCIDRAcceptEmpty = false
	c.ResetRuleMatchCache()
}

func (c *InboundContext) ResetRuleMatchCache() {
	c.SourceAddressMatch = false
	c.SourcePortMatch = false
	c.DestinationAddressMatch = false
	c.DestinationPortMatch = false
	c.DidMatch = false
}

type inboundContextKey struct{}

func WithContext(ctx context.Context, inboundContext *InboundContext) context.Context {
	return context.WithValue(ctx, (*inboundContextKey)(nil), inboundContext)
}

func ContextFrom(ctx context.Context) *InboundContext {
	metadata := ctx.Value((*inboundContextKey)(nil))
	if metadata == nil {
		return nil
	}
	return metadata.(*InboundContext)
}

func ExtendContext(ctx context.Context) (context.Context, *InboundContext) {
	var newMetadata InboundContext
	if metadata := ContextFrom(ctx); metadata != nil {
		newMetadata = *metadata
	}
	return WithContext(ctx, &newMetadata), &newMetadata
}

func OverrideContext(ctx context.Context) context.Context {
	if metadata := ContextFrom(ctx); metadata != nil {
		newMetadata := *metadata
		return WithContext(ctx, &newMetadata)
	}
	return ctx
}
