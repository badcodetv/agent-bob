// Dataset download tokens: the short-lived, name-scoped credential handed to
// an agent running inside a session container so its `curl` can pull a
// dataset's bytes over Orange's HTTP API without carrying the project's own
// API key into the container (design/2026-08-20-agent-wolf.md, O4).
//
// Modelled directly on embedtoken.go: same signing secret
// (AGENTKIT_JWT_SECRET), same deliberately-empty `sid` claim, same
// clamp-never-reject TTL treatment. The only real difference is what the
// token is scoped to — a (project, name) pair via devclaims.DatasetScope,
// instead of one session id.
//
// Only the mint helper and the verify function live here. O5 calls
// verifyDatasetToken from agentd's auth middleware, never from go/httpapi,
// which stays free of JWT code. The route that actually mints a token for an
// agent to use, and the ?token= middleware leg that lets a dataset token
// authenticate a download request, both belong to later tickets (O5, O6b) —
// this file is the mint/verify pair they build on.
package main

import (
	"context"
	"errors"
	"time"

	"github.com/binocarlos/badcode-agent-orange/extension"
	"github.com/binocarlos/badcode-agent-orange/extension/devclaims"
	"github.com/golang-jwt/jwt/v5"
)

// TTL bounds, in seconds. Shorter than an embed token's (default 300s vs
// 900s, ceiling 900s vs 3600s): this credential is handed to a MODEL running
// inside a container, reachable by anything that can get the model to repeat
// a URL, so its window of usefulness to an attacker is kept tight.
const (
	datasetTokenDefaultTTL = 300 * time.Second
	datasetTokenMinTTL     = 60 * time.Second
	datasetTokenMaxTTL     = 900 * time.Second
)

// clampDatasetTTL applies the [60s, 900s] bound. Clamped, never rejected —
// same reasoning as clampEmbedTTL: a caller asking for too little or too much
// gets a working token at the nearest bound, not a 400 discovered in
// production. A zero (absent, or literally 0) is the default rather than the
// floor, because "I did not choose" and "I chose 0" are different statements
// JSON cannot otherwise distinguish without a pointer.
func clampDatasetTTL(seconds int) time.Duration {
	if seconds == 0 {
		return datasetTokenDefaultTTL
	}
	ttl := time.Duration(seconds) * time.Second
	if ttl < datasetTokenMinTTL {
		return datasetTokenMinTTL
	}
	if ttl > datasetTokenMaxTTL {
		return datasetTokenMaxTTL
	}
	return ttl
}

// mintDatasetToken signs a token scoped to one dataset NAME within one
// project — never a version, never a dataset id (devclaims.DatasetScope's own
// comment explains why: a URL minted before a tick must still resolve to that
// name's requested version after it).
//
// ttlSeconds is clamped, never rejected, to [60s, 900s]; 0 means the default
// (300s).
//
// The returned exp is read back off the token this function just signed
// rather than recomputed — the embedTokenExpiry precedent
// (embedtoken.go:196). A caller promising an expiry the token does not carry
// is the off-by-one that shows up later as a rare, unreproducible 401.
func mintDatasetToken(secret []byte, project, name string, ttlSeconds int) (token string, exp int64, err error) {
	ttl := clampDatasetTTL(ttlSeconds)
	tok, err := devclaims.NewWithTTL(secret, ttl).IssueScoped(context.Background(), extension.ContextScope{
		Customer: project,
		Job:      "dataset-download",
	},
		// The sessionID argument is deliberately EMPTY, becoming an empty `sid`
		// claim. See verifyDatasetToken below and embedtoken.go:159-167: a
		// non-empty sid would make this a working core-MCP credential, since
		// /mcp authenticates a caller by exactly that claim, and this token is
		// handed to a model running inside a container.
		"",
		devclaims.DatasetScope(project, name),
	)
	if err != nil {
		return "", 0, err
	}
	exp, err = datasetTokenExpiry(secret, tok)
	if err != nil {
		return "", 0, err
	}
	return tok, exp, nil
}

// datasetTokenExpiry returns the `exp` claim of a token this process just
// minted. Mirrors embedTokenExpiry (embedtoken.go:196).
func datasetTokenExpiry(secret []byte, token string) (int64, error) {
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"})); err != nil {
		return 0, err
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return 0, errors.New("minted token carries no expiry")
	}
	return exp.Unix(), nil
}

// verifyDatasetToken checks the signature, algorithm, expiry and shape of a
// dataset download token and returns the (project, name) pair it is pinned
// to.
//
// Every failure returns the same shape of error (no claim about WHY beyond
// what the message says) so that O5 can answer 404 rather than leak which
// check failed — an attacker probing a download route should not be able to
// distinguish "expired" from "wrong secret" from "not a dataset token" by the
// response.
func verifyDatasetToken(secret []byte, raw string) (project, name string, err error) {
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || tok == nil || !tok.Valid {
		return "", "", errors.New("dataset token invalid or expired")
	}

	// A non-empty sid would make this token a working core-MCP credential
	// (embedtoken.go:159-167) — mintDatasetToken never sets one, but the
	// verifier must not trust that of an arbitrary bearer of the same secret.
	if sid, _ := claims["sid"].(string); sid != "" {
		return "", "", errors.New("dataset token carries a non-empty session id")
	}

	scopeVal, hasScope := claims[devclaims.ScopeClaim]
	if !hasScope {
		return "", "", errors.New("dataset token carries no scope claim")
	}
	scope, _ := scopeVal.(string)
	proj, nm, ok := devclaims.ParseDatasetScope(scope)
	if !ok {
		return "", "", errors.New("dataset token scope is not a dataset scope")
	}

	// The customer claim and the scope's project half must agree. A dataset
	// token is minted with customer == project (see mintDatasetToken above),
	// so disagreement means the token was tampered with or built by hand —
	// either way it must not be trusted for either project.
	customer, _ := claims["customer"].(string)
	if customer != proj {
		return "", "", errors.New("dataset token customer claim disagrees with scope project")
	}

	return proj, nm, nil
}
