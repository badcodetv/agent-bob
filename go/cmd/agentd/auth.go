package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/binocarlos/badcode-agent-orange/extension"
	"github.com/binocarlos/badcode-agent-orange/extension/devclaims"
	"github.com/binocarlos/badcode-agent-orange/httpapi"
	"github.com/golang-jwt/jwt/v5"
)

// principal is the authenticated identity behind a request, from either
// credential the API accepts.
type principal struct {
	email, customer string
	// embedSession is non-empty only for a token carrying scope
	// "session:<id>" — an embed token, minted for a browser inside a
	// third-party page. It confines the credential to that one session;
	// enforcement lives in httpapi, beside the existing ownership check.
	embedSession string
	// datasetScope is non-empty only for a request authenticated by a dataset
	// download token out of ?token= (see the middleware below). It is spelled
	// "<project>/<name>" — NOT the "dataset:" scope value the token carries —
	// and confines the credential to that one dataset. Enforcement lives in
	// httpapi/datasets.go, on the download route alone.
	datasetScope string
}

type ctxKey struct{}

func contextWithPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func principalFromContext(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(principal)
	return p, ok
}

// extensionScope is a tiny helper used by tests + /dev/token to build a scope.
func extensionScope(email, customer string) extension.ContextScope {
	return extension.ContextScope{UserEmail: email, Customer: customer, Job: "demo-job"}
}

// apiKeyHeader is the header a project's backend sends. It is deliberately not
// Authorization: an API key and a bearer JWT are different credentials with
// different lifetimes, and keeping them in different headers means a proxy or a
// log filter can be taught about one without the other.
const apiKeyHeader = "X-API-Key"

// datasetTokenParam is the query parameter carrying a dataset download token.
//
// A query parameter, and not a header or a fragment, because of who the caller
// is: a model inside a session container running `curl <download_url>`. curl
// cannot send a fragment at all, and a header would force the model to compose
// one from a URL it was handed. It is accepted on ONE route — see the middleware
// — and read as a credential nowhere else in the tree.
const datasetTokenParam = "token"

// datasetScopeBearerPrefix is the namespace devclaims.DatasetScope stamps.
//
// devclaims keeps its own copy unexported (it is a sibling of the session
// prefix), so this is a deliberate second spelling of one security-critical
// string; TestDatasetDownloadAuth_ScopePrefixMatchesDevclaims pins the two
// together so they cannot drift.
const datasetScopeBearerPrefix = "dataset:"

// datasetDownloadPath reports whether r is the one request shape the ?token=
// credential is accepted on: GET /agent/datasets/<exactly one segment>/download.
//
// "Exactly one segment" is load-bearing rather than tidy. Without it a path like
// /agent/datasets/a/b/download would be accepted here and routed somewhere else
// entirely by the mux, and the leg would be authenticating a request it never
// looked at.
func datasetDownloadPath(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/agent/datasets/")
	if !ok {
		return false
	}
	name, ok := strings.CutSuffix(rest, "/download")
	if !ok {
		return false
	}
	return name != "" && !strings.Contains(name, "/")
}

// datasetTokenEmail is the synthetic principal email a dataset token
// authenticates as — obviously not a human, and distinguishable from
// apiKeyEmail's, so anything recording "who did this" records the credential
// class rather than an empty string.
func datasetTokenEmail(project string) string { return "dataset-token:" + project }

// apiAuthMiddleware authenticates every API request from one of three
// credentials:
//
//	X-API-Key: <raw>            a long-lived project key, server-side only
//	Authorization: Bearer <jwt> an HS256 token signed with secret
//	?token=<jwt>                a short-lived DATASET token, and ONLY on
//	                            GET /agent/datasets/{name}/download
//
// The key is tried first, because a caller that sent one meant to use it and
// should get a 401 rather than falling through to an anonymous mode.
//
// All three paths produce the same principal. The JWT path additionally carries
// an optional session scope (see principal.embedSession); the ?token= path
// always carries a dataset scope (principal.datasetScope) and nothing else.
//
// An empty secret still enables dev-open mode — a default principal, no
// verification — for the zero-config demo, but ONLY when no project key is
// configured. A configured key means a real deployment, and dev-open there would
// hand every unauthenticated request the "demo" project.
func apiAuthMiddleware(secret []byte, keys projectKeys, next http.Handler) http.Handler {
	devOpen := len(secret) == 0 && !keys.hasKeys()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw := strings.TrimSpace(r.Header.Get(apiKeyHeader)); raw != "" {
			project, ok := keys.ProjectForKey(raw)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// There is no human behind an API key. The email is a stable,
			// obviously-synthetic label so anything that records "who did this"
			// records the project's key rather than an empty string, which is
			// how a human edit is spelled elsewhere.
			next.ServeHTTP(w, r.WithContext(contextWithPrincipal(
				r.Context(), principal{email: apiKeyEmail(project), customer: project})))
			return
		}
		// The ?token= leg (O5 of design/2026-08-20-agent-wolf.md): the ONE place
		// in this tree where a credential arrives in the URL, and the ONE route
		// it is accepted on.
		//
		// It exists because the caller is an agent inside a session container
		// running `curl <download_url>` — a request carrying neither X-API-Key
		// nor Authorization, which everything below answers 401 before any
		// handler sees it. The token is short-lived (300s by default, ceiling
		// 900s) and pins one (project, name) pair, so the worst a leaked one
		// buys is one dataset's bytes for a few minutes.
		//
		// It sits AFTER the API-key branch — a caller that sent a key meant to
		// use it — and BEFORE dev-open, which needs no credential at all. A
		// verification failure falls through to the ordinary 401 rather than
		// answering something more specific: an attacker probing this route
		// must not be able to tell "expired" from "wrong secret" from "not a
		// dataset token".
		//
		// The token's NAME is not compared to the path here. That comparison is
		// httpapi's, on Identity.DatasetScope, precisely so a mismatch can be
		// answered with the same 404 an absent dataset gets instead of a 401
		// that confirms the name exists.
		if len(secret) > 0 && datasetDownloadPath(r) {
			if raw := strings.TrimSpace(r.URL.Query().Get(datasetTokenParam)); raw != "" {
				if project, name, err := verifyDatasetToken(secret, raw); err == nil {
					next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), principal{
						email:        datasetTokenEmail(project),
						customer:     project,
						datasetScope: project + "/" + name,
					})))
					return
				}
			}
		}
		if devOpen {
			next.ServeHTTP(w, r.WithContext(contextWithPrincipal(
				r.Context(), principal{email: "demo@example.com", customer: "demo"})))
			return
		}
		if len(secret) == 0 {
			// Keys are configured but this request carried none, and there is no
			// secret to verify a bearer token with. Nothing can authenticate it.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims := jwt.MapClaims{}
		tok, err := jwt.ParseWithClaims(auth[7:], claims, func(*jwt.Token) (any, error) {
			return secret, nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !tok.Valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// A token carrying a non-empty `sid` is a SESSION token: the credential
		// agentd injects into a container as SESSION_TOKEN, readable by the
		// harness and therefore reachable by a prompt-injected model. It is a
		// different credential class from an API token and must never
		// authenticate a project route (doc 22, RD30).
		//
		// The two classes are signed with different keys (sessionsecret.go), so
		// a session token cannot reach this line at all. This is the second
		// lock, for the deployment that sets AGENTKIT_SESSION_JWT_SECRET to the
		// API secret and for any future issuer that stamps sid by accident.
		// Every API-class token — login, wildcard-exchange, /dev/token, embed —
		// is issued with an empty session id; embed tokens confine themselves
		// with the scope claim below, never with sid.
		if sid, _ := claims["sid"].(string); sid != "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// A DATASET token presented as a bearer token is refused outright, and
		// for the same reason as the lock above: it is a credential that reaches
		// a container, so it is not API-class.
		//
		// Without this it would authenticate as an UNRESTRICTED project
		// principal on every /agent/* route — a dataset token is signed with
		// this same secret and carries the same empty `sid` as an embed token,
		// and the scope claim is read below only through ParseSessionScope,
		// which says "not a session scope" and leaves the principal wide open.
		// The narrow ?token= leg above is the only way this credential may
		// authenticate anything.
		//
		// The lock is specific to the "dataset:" namespace and NOT to "any scope
		// I do not recognise": a future scope kind gets its own explicit
		// handling, and a token scoped `project:…` still authenticates exactly
		// as it did (TestAuthMiddleware_UnknownScopeKindIsNotASessionScope).
		if v, _ := claims[devclaims.ScopeClaim].(string); strings.HasPrefix(v, datasetScopeBearerPrefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		p := principal{}
		if v, ok := claims["email"].(string); ok {
			p.email = v
		}
		if v, ok := claims["customer"].(string); ok {
			p.customer = v
		}
		if v, ok := claims[devclaims.ScopeClaim].(string); ok {
			if sid, scoped := devclaims.ParseSessionScope(v); scoped {
				p.embedSession = sid
			}
		}
		next.ServeHTTP(w, r.WithContext(contextWithPrincipal(r.Context(), p)))
	})
}

// apiKeyEmail is the synthetic principal email an API key authenticates as.
func apiKeyEmail(project string) string { return "api-key:" + project }

// identityFromRequest is the httpapi.IdentityFunc: it reads what the middleware set.
func identityFromRequest(r *http.Request) (httpapi.Identity, error) {
	p, _ := principalFromContext(r.Context())
	return httpapi.Identity{
		UserEmail:    p.email,
		Customer:     p.customer,
		SessionScope: p.embedSession,
		DatasetScope: p.datasetScope,
	}, nil
}
