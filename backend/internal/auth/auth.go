package auth

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrInvalidToken        = errors.New("invalid access token")
	ErrVerifierUnavailable = errors.New("identity provider unavailable")
)

type Principal struct {
	UserID string
}

type Session struct {
	Principal   Principal
	AccessToken string
}

type Verifier interface {
	Verify(ctx context.Context, accessToken string) (Principal, error)
}

type sessionContextKey struct{}

func WithSession(ctx context.Context, session Session) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, session)
}

func SessionFromContext(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionContextKey{}).(Session)
	return session, ok
}

func ParseBearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
