// Package sourceaddr carries a request's resolved client address in its
// context, for code below the HTTP layer that has no gin context.
package sourceaddr

import (
	"context"
	"net/netip"
)

type key struct{}

// With returns ctx carrying address when it is a valid IP address; anything
// else leaves ctx as it is.
func With(ctx context.Context, address string) context.Context {
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, key{}, addr.Unmap().String())
}

// From returns the address With stored, or "".
func From(ctx context.Context) string {
	address, _ := ctx.Value(key{}).(string)
	return address
}
