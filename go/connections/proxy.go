package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// ErrWorkerGone is what ProxyConfig.Grants returns (alongside an empty list)
// for a worker that is disabled or deleted: it holds nothing, and the proxy
// says so rather than claiming a store failure.
var ErrWorkerGone = errors.New("connections: worker is disabled or gone")

// ErrSessionExpired is what ProxyConfig.Authenticate wraps when the session
// token is well-formed and correctly signed but expired, and the session it
// names is no longer live (archived, completed, errored or deleted) — the
// "a stolen token dies with the session" rule (design decision 3). Any other
// Authenticate error is reported as a plain rejection.
var ErrSessionExpired = errors.New("connections: session token expired and the session is not live")

// Caller is who is on the other end of a /connect/ request, as established
// by the session token — never by a header the container chose. Worker is
// the session row's worker ("" for a plain chat session).
type Caller struct{ Project, SessionID, Worker string }

// ProxyConfig is everything the /connect/ handler needs from its host.
type ProxyConfig struct {
	Registry *Registry
	// Authenticate verifies the session token on the request. Live-session
	// checking belongs here (the host has the session store), not in this
	// package; wrap ErrSessionExpired to get the "expired" 401. nil refuses
	// every request.
	Authenticate func(*http.Request) (Caller, error)
	// Grants returns the worker's current grant list. It is called on every
	// request, so revoking a grant takes effect mid-session. A disabled or
	// deleted worker returns ErrWorkerGone; any other error is a 503. nil
	// fails every request with 503.
	Grants    func(ctx context.Context, project, worker string) ([]string, error)
	Transport http.RoundTripper // nil → http.DefaultTransport
	Logf      func(string, ...any)
}

// connectPrefix is the route the proxy is mounted at.
const connectPrefix = "/connect/"

// forwardedRequestHeaders is the allowlist of inbound headers that reach the
// upstream — what Streamable HTTP MCP needs, and nothing else. Everything the
// container sends besides these (its session token in Authorization, any
// Cookie, X-Api-Key, X-Session-Id, forwarding headers, User-Agent) is dropped;
// an allowlist means a header added to the container side later cannot leak
// by default.
var forwardedRequestHeaders = []string{
	"Accept",
	"Content-Type",
	"Mcp-Session-Id",
	"Mcp-Protocol-Version",
	"Last-Event-ID",
}

// strippedResponseHeaders never reach the container: WWW-Authenticate would
// describe the upstream's auth scheme to a client that has no business
// negotiating it, and an upstream cookie has no business in a session.
var strippedResponseHeaders = []string{"WWW-Authenticate", "Set-Cookie"}

// NewProxy serves paths of the form /connect/{name}/{rest...}: it
// authenticates the session, re-reads the session worker's grants, and
// streams the request to connection {name}'s upstream with the session's
// credentials removed and the real one added
// (design/2026-09-11-project-connections.md, "Proxy behaviour").
func NewProxy(cfg ProxyConfig) http.Handler {
	p := &proxy{cfg: cfg, transport: cfg.Transport, logf: cfg.Logf}
	if p.transport == nil {
		p.transport = http.DefaultTransport
	}
	if p.logf == nil {
		p.logf = func(string, ...any) {}
	}
	return p
}

type proxy struct {
	cfg       ProxyConfig
	transport http.RoundTripper
	logf      func(string, ...any)
}

// proxyError is a refusal decided after the upstream answered (in
// ModifyResponse), carried to the ErrorHandler.
type proxyError struct {
	status int
	msg    string
}

func (e *proxyError) Error() string { return e.msg }

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w}
	var (
		caller         Caller
		name           string
		upstreamStatus int
		note           string
	)
	// One line per request, deferred so it is written even when the stream
	// is aborted mid-copy. Never a header value: the session token and the
	// real credential both travel in headers.
	defer func() {
		line := fmt.Sprintf("connect: project=%q session=%q worker=%q connection=%q method=%s status=%d upstream_status=%d duration=%s",
			caller.Project, caller.SessionID, caller.Worker, name, r.Method, sw.code(), upstreamStatus,
			time.Since(start).Round(time.Millisecond))
		if note != "" {
			line += " note=" + fmt.Sprintf("%q", note)
		}
		p.logf("%s", line)
	}()

	if p.cfg.Authenticate == nil {
		writeError(sw, http.StatusUnauthorized, "session token rejected")
		return
	}
	c, err := p.cfg.Authenticate(r)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) {
			writeError(sw, http.StatusUnauthorized, "session token expired")
		} else {
			writeError(sw, http.StatusUnauthorized, "session token rejected")
		}
		return
	}
	caller = c

	n, rest, ok := splitConnectPath(r.URL.EscapedPath())
	if !ok {
		writeError(sw, http.StatusBadRequest, "invalid path")
		return
	}
	name = n

	if caller.Worker == "" {
		writeError(sw, http.StatusForbidden, "this session is not running as a worker; connections are granted to workers")
		return
	}
	var grants []string
	if p.cfg.Grants == nil {
		err = errors.New("no grant lookup configured")
	} else {
		grants, err = p.cfg.Grants(r.Context(), caller.Project, caller.Worker)
	}
	switch {
	case errors.Is(err, ErrWorkerGone):
		writeError(sw, http.StatusForbidden, fmt.Sprintf("worker %s is disabled or gone, and holds no connections", caller.Worker))
		return
	case err != nil:
		note = err.Error()
		writeError(sw, http.StatusServiceUnavailable, fmt.Sprintf("could not read worker %s's connections; retry", caller.Worker))
		return
	}
	if !Holds(grants, name) {
		writeError(sw, http.StatusForbidden, fmt.Sprintf("worker %s does not hold connection %s (connection_list shows what exists)", caller.Worker, name))
		return
	}

	conn, ok := p.cfg.Registry.Get(caller.Project, name)
	if !ok {
		writeError(sw, http.StatusNotFound, fmt.Sprintf("no connection %s in this project", name))
		return
	}
	if ok, reason := p.cfg.Registry.Availability(caller.Project, name); !ok {
		writeError(sw, http.StatusServiceUnavailable, reason)
		return
	}
	var token string
	if conn.Spec.Auth.Type == AuthGoogleAccount {
		// Looked up at use, not at boot: a Connect or Disconnect in the
		// console takes effect on this very request (A11).
		tok, status, msg, err := p.cfg.Registry.accountToken(r.Context(), conn)
		if status != 0 {
			if err != nil {
				note = err.Error()
			}
			writeError(sw, status, msg)
			return
		}
		token = tok
	} else {
		tok, err := conn.Token.Token(r.Context())
		if err != nil {
			if errors.Is(err, ErrCredentialRevoked) {
				writeError(sw, http.StatusBadGateway, fmt.Sprintf("Google refused the refresh token for %s — get a new one: docs/22-connections.md §Google", name))
			} else {
				note = err.Error()
				writeError(sw, http.StatusBadGateway, fmt.Sprintf("could not obtain a credential for %s; retry", name))
			}
			return
		}
		token = tok
	}

	// An empty {rest} is the spec URL exactly — "/connect/gmail/" must reach
	// ".../mcp/v1", not ".../mcp/v1/".
	target := conn.URL
	if rest != "" {
		target = conn.URL.JoinPath(rest)
	}

	rp := &httputil.ReverseProxy{
		Transport:     p.transport,
		FlushInterval: -1, // SSE: every write reaches the container at once
		Rewrite: func(pr *httputil.ProxyRequest) {
			// SetURL sets scheme, host and the merged query, and clears
			// Out.Host so the upstream sees its own Host. It also joins the
			// inbound path onto target's, which is not this route's rule
			// (only {rest} is joined), so the computed path is put back.
			pr.SetURL(target)
			pr.Out.URL.Path = target.Path
			pr.Out.URL.RawPath = target.RawPath

			hdr := make(http.Header, len(forwardedRequestHeaders)+1)
			for _, k := range forwardedRequestHeaders {
				if vs := pr.Out.Header.Values(k); len(vs) > 0 {
					hdr[http.CanonicalHeaderKey(k)] = append([]string(nil), vs...)
				}
			}
			hdr.Set("Authorization", "Bearer "+token)
			pr.Out.Header = hdr
		},
		ModifyResponse: func(resp *http.Response) error {
			upstreamStatus = resp.StatusCode
			for _, k := range strippedResponseHeaders {
				resp.Header.Del(k)
			}
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return &proxyError{http.StatusBadGateway, "upstream rejected the credential for " + name}
			}
			// The transport never follows a redirect, but the client might:
			// a Location on another origin is turned into a 502 so nothing
			// is ever steered away from the connection's upstream.
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				if loc := resp.Header.Get("Location"); loc != "" {
					u, err := resp.Request.URL.Parse(loc)
					if err != nil || !sameOrigin(u, conn.URL) {
						return &proxyError{http.StatusBadGateway, "upstream redirected off-host"}
					}
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			var pe *proxyError
			if errors.As(err, &pe) {
				writeError(w, pe.status, pe.msg)
				return
			}
			note = err.Error()
			writeError(w, http.StatusBadGateway, fmt.Sprintf("could not reach the upstream for %s", name))
		},
	}
	rp.ServeHTTP(sw, r)
}

// splitConnectPath splits an escaped request path "/connect/{name}/{rest...}"
// into the connection name (unescaped) and rest (still escaped, for
// url.JoinPath). It refuses — ok=false — anything that could walk out of the
// connection's base path once joined: a "." or ".." segment (also when
// percent-encoded) or an encoded "/", which would otherwise split into two
// segments only after this check.
func splitConnectPath(escaped string) (name, rest string, ok bool) {
	tail, found := strings.CutPrefix(escaped, connectPrefix)
	if !found {
		return "", "", false
	}
	if strings.Contains(strings.ToLower(tail), "%2f") {
		return "", "", false
	}
	segs := strings.Split(tail, "/")
	for _, seg := range segs {
		dec, err := url.PathUnescape(seg)
		if err != nil || dec == "." || dec == ".." {
			return "", "", false
		}
	}
	name, err := url.PathUnescape(segs[0])
	if err != nil || name == "" {
		return "", "", false
	}
	return name, strings.Join(segs[1:], "/"), true
}

// sameOrigin reports whether u points at the same scheme, host and port as
// base (default ports made explicit), i.e. whether following it could only
// reach the connection's own upstream.
func sameOrigin(u, base *url.URL) bool {
	if !strings.EqualFold(u.Scheme, base.Scheme) {
		return false
	}
	return hostPort(u) == hostPort(base)
}

func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// writeError writes the proxy's refusal shape: {"error": "..."}.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// statusWriter records the status actually sent, for the log line. Unwrap
// lets http.ResponseController (which ReverseProxy flushes through) reach
// the underlying writer's Flush.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusWriter) code() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}
