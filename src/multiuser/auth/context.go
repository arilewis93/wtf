package auth

import (
	"context"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/store"
)

type ctxKey struct{}

func WithUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func UserFrom(ctx context.Context) (*store.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*store.User)
	return u, ok && u != nil
}

type actorKey struct{}

// WithActor marks ctx as an action an admin is taking on the context's user's
// behalf (the user in WithUser is the one acted on). Audit lines read it to
// name who did it. It grants nothing: ownership checks still use WithUser.
func WithActor(ctx context.Context, admin *store.User) context.Context {
	return context.WithValue(ctx, actorKey{}, admin)
}

func ActorFrom(ctx context.Context) (*store.User, bool) {
	u, ok := ctx.Value(actorKey{}).(*store.User)
	return u, ok && u != nil
}

type keyScopeKey struct{}

// WithKeyScope marks ctx as authenticated by an API key of this scope
// (store.ScopeFull or store.ScopeProvisioning). APIKey() sets it for every
// key-authenticated request; a cookie session never sets it at all, so
// KeyScopeFrom reports false for an ordinary signed-in person.
func WithKeyScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, keyScopeKey{}, scope)
}

// KeyScopeFrom mirrors UserFrom: it reports the scope an API key
// authenticated this request with, or ("", false) when the request was not
// authenticated by a key at all (a cookie session, or nothing).
func KeyScopeFrom(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(keyScopeKey{}).(string)
	return s, ok && s != ""
}
