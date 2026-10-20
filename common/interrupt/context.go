package interrupt

import (
	"context"
	"slices"
)

type contextKeyIsExternalConnection struct{}

func ContextWithIsExternalConnection(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKeyIsExternalConnection{}, true)
}

func IsExternalConnectionFromContext(ctx context.Context) bool {
	return ctx.Value(contextKeyIsExternalConnection{}) != nil
}

type contextKeyGroups struct{}

// ContextWithGroup has the connection manager track this flow in g once its
// handshake succeeds, so g.Interrupt closes routed flows too. Nested groups
// accumulate. Interrupt misses flows still dialing; Retire catches them when
// they are tracked. Outbounds that copy flows themselves instead of handing
// them to the connection manager are not tracked.
func ContextWithGroup(ctx context.Context, g *Group) context.Context {
	groups := groupsFromContext(ctx)
	if g == nil || slices.Contains(groups, g) {
		return ctx
	}
	return context.WithValue(ctx, contextKeyGroups{}, append(slices.Clip(groups), g))
}

// GroupsFromContext returns a copy of the groups carried by ctx.
func GroupsFromContext(ctx context.Context) []*Group {
	return slices.Clone(groupsFromContext(ctx))
}

func groupsFromContext(ctx context.Context) []*Group {
	groups, _ := ctx.Value(contextKeyGroups{}).([]*Group)
	return groups
}

// AnyRetired reports whether any group carried by ctx has been retired, so a
// flow can fail its handshake instead of connecting and being reset.
func AnyRetired(ctx context.Context) bool {
	return slices.ContainsFunc(groupsFromContext(ctx), (*Group).Retired)
}
