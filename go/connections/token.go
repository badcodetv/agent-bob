package connections

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/oauth2"
)

// TokenSource returns the bare bearer token to send upstream (never
// "Bearer <token>" — the proxy adds that prefix itself).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// staticToken is a TokenSource that always returns the same token — the
// bearer auth case, where the token IS the credential (nothing to refresh).
type staticToken string

func (s staticToken) Token(ctx context.Context) (string, error) { return string(s), nil }

// StaticToken wraps a fixed bearer token as a TokenSource.
func StaticToken(tok string) TokenSource { return staticToken(tok) }

// defaultGoogleTokenURL is Google's OAuth 2.0 token endpoint, used whenever a
// Spec does not override it (which the project map cannot do — see Auth's
// tokenURL comment).
const defaultGoogleTokenURL = "https://oauth2.googleapis.com/token"

// ErrCredentialRevoked is returned by a google_oauth TokenSource when the
// upstream refuses the refresh token itself (RFC 6749 "invalid_grant" —
// revoked, expired, or the OAuth consent screen is still in Testing, where
// Google expires refresh tokens after 7 days). Getting a fresh one is a
// manual step; see docs/22-connections.md.
var ErrCredentialRevoked = errors.New("connections: upstream refused the refresh token (invalid_grant)")

// googleTokenSource builds a TokenSource for a resolved google_oauth spec, or
// returns a non-empty reason when it cannot — that reason becomes the
// connection's Unavailable. Indirected through a package variable (rather
// than NewRegistry calling GoogleRefresh directly) so go/connections/spec.go
// and this constructor could ship independently; both now live here.
var googleTokenSource = func(clientID, clientSecret, refreshToken, tokenURL string) (TokenSource, string) {
	return GoogleRefresh(clientID, clientSecret, refreshToken, tokenURL), ""
}

// GoogleRefresh returns a TokenSource that exchanges refreshToken for a
// short-lived access token on first use and caches it until shortly before
// expiry, refreshing again only once expired. Concurrent callers share one
// refresh: oauth2.Config's reuse token source holds its lock for the whole
// exchange, so the second-through-Nth caller blocks and then observes the
// token the first caller just fetched, rather than each firing its own
// request. tokenURL "" uses Google's token endpoint.
func GoogleRefresh(clientID, clientSecret, refreshToken, tokenURL string) TokenSource {
	if tokenURL == "" {
		tokenURL = defaultGoogleTokenURL
	}
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     oauth2.Endpoint{TokenURL: tokenURL},
	}
	// Token{RefreshToken: ...} carries no AccessToken, so Valid() is false
	// and the first Token() call always exchanges — there is no separate
	// "seed" access token to start from, only the refresh token.
	src := cfg.TokenSource(context.Background(), &oauth2.Token{RefreshToken: refreshToken})
	return &googleRefreshSource{src: src}
}

// googleRefreshSource adapts oauth2.TokenSource (Token() (*oauth2.Token,
// error), no context — the library fixes its context at construction) to
// this package's TokenSource (Token(ctx) (string, error)).
type googleRefreshSource struct {
	src oauth2.TokenSource
}

func (g *googleRefreshSource) Token(ctx context.Context) (string, error) {
	tok, err := g.src.Token()
	if err != nil {
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant" {
			return "", ErrCredentialRevoked
		}
		// oauth2's error already omits the refresh token (it reports the
		// response status/body, not the request); still never wrap raw
		// request parameters here.
		return "", fmt.Errorf("connections: google token refresh failed: %w", err)
	}
	return tok.AccessToken, nil
}
