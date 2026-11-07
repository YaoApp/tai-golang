package main

import "context"

type demoUserCtxKey struct{}

// withDemoUser stores the logged-in account in the request context.
func withDemoUser(ctx context.Context, user *DemoUser) context.Context {
	return context.WithValue(ctx, demoUserCtxKey{}, user)
}

// demoUserFromContext returns the logged-in account, if any.
func demoUserFromContext(ctx context.Context) (*DemoUser, bool) {
	user, ok := ctx.Value(demoUserCtxKey{}).(*DemoUser)
	return user, ok && user != nil
}
