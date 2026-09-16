package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/connections"
	"golang.org/x/oauth2"
)

// googleconnect.go — the four HTTP routes of Connect Google
// (design/2026-09-11-project-connections.md, "Addendum 2026-09-16", T23):
//
//	GET    /agent/connections                  apiMux  the Settings panel's list
//	POST   /agent/connections/{account}/connect apiMux  A4 → {authorize_url} + cookie
//	DELETE /agent/connections/{account}        apiMux  A4 → best-effort revoke → {revoked}
//	GET    /auth/connections/google/callback    root    the browser back from Google → 303
//
// The state, pending connects, PKCE and the authorize URL are
// googleconnect_state.go's; the use-time token lookup is googleaccounts.go's.
// What lives here is the order of the checks and what each outcome says.
//
// Logging: one line per start, callback outcome and disconnect, naming the
// project, account, user email and result/reason. Never the code, state,
// cookie, a token, the ID token or ciphertext — and never an error from the
// exchange or tokeninfo verbatim, because those can quote a URL carrying one.

// googleConnectCookie binds a connect to the browser that started it (A6.1).
const googleConnectCookie = "bob_connect"

// googleConnectCookiePath scopes the cookie to the callback's prefix.
const googleConnectCookiePath = "/auth/connections/"

// googleConnectProvider is the only provider there is.
const googleConnectProvider = "google"

// googleConnectHTTPTimeout bounds each call to Google (exchange, tokeninfo,
// revoke) so a hung Google cannot hold a request open indefinitely.
const googleConnectHTTPTimeout = 15 * time.Second

// Callback reason codes: a closed set, never Google's own text (addendum,
// HTTP table). state_invalid is the HTML page, not a redirect.
const (
	connectReasonCancelled      = "cancelled"
	connectReasonExpired        = "expired"
	connectReasonOtherBrowser   = "other_browser"
	connectReasonNotAllowed     = "not_allowed"
	connectReasonNoRefreshToken = "no_refresh_token"
	connectReasonMissingScopes  = "missing_scopes"
	connectReasonExchangeFailed = "exchange_failed"
	connectReasonStoreFailed    = "store_failed"
)

// googleConnectStore is the store surface the routes need. *agentdb.Store
// satisfies it; Put and Delete write the connection_connect/_disconnect
// config events themselves (T17).
type googleConnectStore interface {
	GetConnectionCredential(ctx context.Context, project, account string) (*agentdb.ConnectionCredential, error)
	ListConnectionCredentials(ctx context.Context, project string) ([]*agentdb.ConnectionCredential, error)
	PutConnectionCredential(ctx context.Context, c *agentdb.ConnectionCredential) error
	DeleteConnectionCredential(ctx context.Context, project, account, by string) error
}

var _ googleConnectStore = (*agentdb.Store)(nil)

// accountInvalidator is googleAccounts.Invalidate: called right after a
// connect or disconnect commits so the proxy sees it on the next request.
type accountInvalidator interface {
	Invalidate(project, account string)
}

// googleConnectDeps is everything registerGoogleConnect needs. store must be
// non-nil (T24 registers only when agentDB != nil). accounts is nil when
// Connect Google is disabled (no googleAccounts was built). stillOperator is
// the callback's A4 re-check against the CURRENT project map —
// connectOperatorRecheck builds it.
type googleConnectDeps struct {
	cfg           googleConnectConfig
	registry      *connections.Registry
	store         googleConnectStore
	accounts      accountInvalidator
	stillOperator func(project, email string) bool
	jwtSecretSet  bool
	logf          func(string, ...any)

	// Test seams; zero values mean time.Now, a fresh pendingConnects and a
	// default client.
	now        func() time.Time
	pending    *pendingConnects
	httpClient *http.Client
}

// registerGoogleConnect mounts the three API routes on apiMux (behind
// apiAuthMiddleware) and the callback on root (unauthenticated: the browser
// arrives from accounts.google.com with a cookie, not a bearer token).
func registerGoogleConnect(apiMux, root *http.ServeMux, deps googleConnectDeps) {
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.pending == nil {
		deps.pending = newPendingConnects()
	}
	deps.pending.now = deps.now
	if deps.httpClient == nil {
		deps.httpClient = &http.Client{Timeout: googleConnectHTTPTimeout}
	}
	if deps.logf == nil {
		deps.logf = func(string, ...any) {}
	}
	if deps.stillOperator == nil {
		deps.stillOperator = func(string, string) bool { return false }
	}
	h := &googleConnect{googleConnectDeps: deps}
	apiMux.HandleFunc("GET /agent/connections", h.list)
	apiMux.HandleFunc("POST /agent/connections/{account}/connect", h.connect)
	apiMux.HandleFunc("DELETE /agent/connections/{account}", h.disconnect)
	root.HandleFunc("GET "+googleConnectCallbackPath, h.callback)
}

type googleConnect struct{ googleConnectDeps }

// wireGoogleConnect is T24: main.go's one call, made once the Registry, the
// project map holder, jwtSecret and Connect Google's boot config
// (loadGoogleConnectConfig) all exist. It installs the AccountSource on reg
// — a working googleAccounts, or a reason nothing can use google_account
// connections yet — and, only when a store exists, mounts the four routes
// via registerGoogleConnect.
//
// store follows the same typed-nil rule mountConnectRoute (connections.go)
// already applies: a nil *agentdb.Store handed in as the googleConnectStore
// interface is treated as "no store", never boxed into a non-nil interface
// that lies about it (the T13 nil-store trap this package keeps re-stating).
// That is what lets main.go pass agentDB straight through, on the sqlite
// fallback or on Postgres, without its own nil check.
func wireGoogleConnect(apiMux, root *http.ServeMux, store googleConnectStore, reg *connections.Registry, cfg googleConnectConfig, settings *projectSettingsHolder, testLoginEmail string, jwtSecret []byte, logf func(string, ...any)) {
	if s, ok := store.(*agentdb.Store); ok && s == nil {
		store = nil
	}

	disabledReason := cfg.disabledReason
	if store == nil {
		disabledReason = "Connect Google needs DATABASE_URL"
	}

	var accounts *googleAccounts
	if store != nil && cfg.enabled() {
		accounts = newGoogleAccounts(store, cfg, logf)
		reg.SetAccounts(accounts, "")
		logf("[agentd] connect google: enabled (redirect %s)", cfg.redirectURI())
	} else {
		reg.SetAccounts(nil, disabledReason)
		logf("[agentd] connect google: DISABLED (%s)", disabledReason)
	}

	if store == nil {
		return
	}
	// accounts may be nil here (store set but cfg disabled): accountInvalidator
	// must then be a true nil interface, never a *googleAccounts(nil) boxed
	// into one — the same trap this file's doc comment on accountInvalidator
	// names.
	var inv accountInvalidator
	if accounts != nil {
		inv = accounts
	}
	registerGoogleConnect(apiMux, root, googleConnectDeps{
		cfg:           cfg,
		registry:      reg,
		store:         store,
		accounts:      inv,
		stillOperator: connectOperatorRecheck(settings, testLoginEmail),
		jwtSecretSet:  len(jwtSecret) > 0,
		logf:          logf,
	})
}

// connectOperatorRecheck answers A4's callback question — does email still
// hold operator authority for project — from the live holder on every call.
// A wildcard users entry, or the AGENTKIT_TEST_LOGIN email (an implicit
// wildcard with no users entry, see authPasswordHandler), is an operator
// everywhere; anyone else only through the project's operators list.
func connectOperatorRecheck(settings *projectSettingsHolder, testLoginEmail string) func(project, email string) bool {
	testLoginEmail = strings.ToLower(strings.TrimSpace(testLoginEmail))
	return func(project, email string) bool {
		email = strings.ToLower(email)
		if testLoginEmail != "" && email == testLoginEmail {
			return true
		}
		_, wildcard, _ := settings.resolve(email)
		return settings.isOperator(project, email, wildcard)
	}
}

// ── A4 ────────────────────────────────────────────────────────────────────────

var (
	errConnectDevOpen    = errors.New("Connect Google needs console login: AGENTKIT_JWT_SECRET is not set on this agentd")
	errConnectNotAllowed = errors.New("only a project operator signed in to the console may connect or disconnect Google")
	errConnectNoProject  = errors.New("no project in token")
)

// connectAuthority is A4: a console login JWT carrying the operator claim for
// one concrete project, and only when AGENTKIT_JWT_SECRET is set. An API key
// is an operator for budgets but not here; an embed or dataset token never.
func connectAuthority(r *http.Request, jwtSecretSet bool) (email, project string, err error) {
	if !jwtSecretSet {
		return "", "", errConnectDevOpen
	}
	p, _ := principalFromContext(r.Context())
	if p.customer == "" || p.customer == projectWildcard {
		return "", "", errConnectNoProject
	}
	if !p.operator || p.apiKey || p.embedSession != "" || p.datasetScope != "" || p.email == "" {
		return "", "", errConnectNotAllowed
	}
	return strings.ToLower(p.email), p.customer, nil
}

// ── GET /agent/connections ────────────────────────────────────────────────────

type connectionRowJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Account     string `json:"account,omitempty"`
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable,omitempty"`
}

type accountRowJSON struct {
	Account      string   `json:"account"`
	Provider     string   `json:"provider"`
	Connected    bool     `json:"connected"`
	AccountEmail string   `json:"account_email,omitempty"`
	ConnectedBy  string   `json:"connected_by,omitempty"`
	ConnectedAt  int64    `json:"connected_at,omitempty"`
	Unavailable  string   `json:"unavailable,omitempty"`
	Connections  []string `json:"connections"`
}

type connectionsListJSON struct {
	Connections           []connectionRowJSON `json:"connections"`
	Accounts              []accountRowJSON    `json:"accounts"`
	CanConnect            bool                `json:"can_connect"`
	ConnectDisabledReason string              `json:"connect_disabled_reason,omitempty"`
}

// list is built field by field from the Registry and the rows' metadata: a
// stored row is never encoded, so no future column can reach the body.
func (h *googleConnect) list(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFromContext(r.Context())
	if p.embedSession != "" || p.datasetScope != "" {
		writeConnectError(w, http.StatusForbidden, "this credential cannot read connections")
		return
	}
	if p.customer == "" || p.customer == projectWildcard {
		writeConnectError(w, http.StatusForbidden, errConnectNoProject.Error())
		return
	}
	project := p.customer

	out := connectionsListJSON{Connections: []connectionRowJSON{}, Accounts: []accountRowJSON{}}
	for _, info := range h.registry.List(project) {
		row := connectionRowJSON{Name: info.Name, Description: info.Description, Available: info.Available, Unavailable: info.Unavailable}
		if conn, ok := h.registry.Get(project, info.Name); ok && conn.Spec.Auth.Type == connections.AuthGoogleAccount {
			row.Account = conn.Spec.Auth.Account
		}
		out.Connections = append(out.Connections, row)
	}

	if declared := h.registry.Accounts(project); len(declared) > 0 {
		creds, err := h.store.ListConnectionCredentials(r.Context(), project)
		if err != nil {
			h.logf("[agentd] connect google: list project=%q: %v", project, err)
			writeConnectError(w, http.StatusInternalServerError, "could not read the stored connections")
			return
		}
		byAccount := map[string]*agentdb.ConnectionCredential{}
		for _, c := range creds {
			byAccount[c.Account] = c
		}
		for _, a := range declared {
			row := accountRowJSON{Account: a.Account, Provider: googleConnectProvider, Connections: a.Connections}
			if c, ok := byAccount[a.Account]; ok {
				row.Connected = true
				row.AccountEmail = c.AccountEmail
				row.ConnectedBy = c.ConnectedBy
				row.ConnectedAt = c.ConnectedAt
				switch {
				case !h.cfg.enabled():
					row.Unavailable = h.cfg.disabledReason
				case c.KeyID != h.cfg.sealer.KeyID():
					row.Unavailable = connections.KeyChangedReason(a.Account)
				}
			}
			out.Accounts = append(out.Accounts, row)
		}
	}

	// The reason names the first thing standing in the way, most structural
	// first: no login at all, then the deployment, then this person.
	if _, _, err := connectAuthority(r, h.jwtSecretSet); errors.Is(err, errConnectDevOpen) {
		out.ConnectDisabledReason = err.Error()
	} else if !h.cfg.enabled() {
		out.ConnectDisabledReason = h.cfg.disabledReason
	} else if err != nil {
		out.ConnectDisabledReason = err.Error()
	} else {
		out.CanConnect = true
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// ── POST /agent/connections/{account}/connect ─────────────────────────────────

func (h *googleConnect) connect(w http.ResponseWriter, r *http.Request) {
	email, project, err := connectAuthority(r, h.jwtSecretSet)
	if err != nil {
		writeConnectError(w, http.StatusForbidden, err.Error())
		return
	}
	account := r.PathValue("account")
	if !h.declares(project, account) {
		writeConnectError(w, http.StatusNotFound, fmt.Sprintf("this project declares no Google account named %q", account))
		return
	}
	if !h.cfg.enabled() {
		writeConnectError(w, http.StatusServiceUnavailable, h.cfg.disabledReason)
		return
	}

	nonce, err := newConnectNonce()
	if err != nil {
		h.logf("[agentd] connect google: start project=%q account=%q user=%q result=error: %v", project, account, email, err)
		writeConnectError(w, http.StatusInternalServerError, "could not start Connect Google")
		return
	}
	verifier := newPKCEVerifier()
	now := h.now()
	exp := now.Add(googleConnectTTL)
	if err := h.pending.put(nonce, pendingConnect{project: project, account: account, email: email, verifier: verifier, exp: exp}); err != nil {
		h.logf("[agentd] connect google: start project=%q account=%q user=%q result=error: %v", project, account, email, err)
		writeConnectError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	state, err := signState(h.cfg.sealer.StateKey(), connectState{Project: project, Account: account, Email: email, Nonce: nonce, Exp: exp.Unix()})
	if err != nil {
		h.pending.take(nonce)
		h.logf("[agentd] connect google: start project=%q account=%q user=%q result=error: %v", project, account, email, err)
		writeConnectError(w, http.StatusInternalServerError, "could not start Connect Google")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     googleConnectCookie,
		Value:    nonce,
		Path:     googleConnectCookiePath,
		MaxAge:   int(googleConnectTTL / time.Second),
		HttpOnly: true,
		Secure:   h.secureCookie(),
		SameSite: http.SameSiteLaxMode,
	})
	h.logf("[agentd] connect google: start project=%q account=%q user=%q", project, account, email)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"authorize_url": authorizeURL(h.cfg, state, pkceChallenge(verifier))})
}

// declares reports whether project has a google_account connection naming
// account — read through the Registry, where the "google" default is filled
// in (T19), never from the raw map.
func (h *googleConnect) declares(project, account string) bool {
	for _, a := range h.registry.Accounts(project) {
		if a.Account == account {
			return true
		}
	}
	return false
}

func (h *googleConnect) secureCookie() bool {
	return strings.HasPrefix(strings.ToLower(h.cfg.publicBase), "https://")
}

// ── GET /auth/connections/google/callback ─────────────────────────────────────

// callback runs A6's checks in the order that matters: signature → expiry →
// cookie == nonce → take the pending entry (so a wrong-browser callback does
// not consume it) → Google's error param → exchange → refresh token → ID
// token → scopes → the operator re-check → seal and store.
func (h *googleConnect) callback(w http.ResponseWriter, r *http.Request) {
	// The callback URL carries the code and state; nothing this response
	// leads to may send it on as a Referer.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()

	if !h.cfg.enabled() {
		h.logf("[agentd] connect google: callback result=error reason=state_invalid (Connect Google is disabled)")
		h.invalidStatePage(w)
		return
	}
	st, err := verifyState(h.cfg.sealer.StateKey(), q.Get("state"), h.now())
	switch {
	case errors.Is(err, errStateExpired):
		h.finish(w, st, connectReasonExpired, "")
		return
	case err != nil:
		h.logf("[agentd] connect google: callback result=error reason=state_invalid")
		h.invalidStatePage(w)
		return
	}

	c, cerr := r.Cookie(googleConnectCookie)
	if cerr != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(st.Nonce)) != 1 {
		h.finish(w, st, connectReasonOtherBrowser, "")
		return
	}
	// From here the cookie has done its job, whatever the outcome.
	h.clearCookie(w)

	pc, ok := h.pending.take(st.Nonce)
	if !ok || pc.project != st.Project || pc.account != st.Account || pc.email != st.Email {
		h.finish(w, st, connectReasonExpired, "")
		return
	}

	if e := q.Get("error"); e != "" {
		reason := connectReasonExchangeFailed
		if e == "access_denied" {
			reason = connectReasonCancelled
		}
		h.finish(w, st, reason, "google returned an error")
		return
	}
	code := q.Get("code")
	if code == "" {
		h.finish(w, st, connectReasonExchangeFailed, "no code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*googleConnectHTTPTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, h.httpClient)
	oc := &oauth2.Config{
		ClientID:     h.cfg.clientID,
		ClientSecret: h.cfg.clientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: h.cfg.authURL, TokenURL: h.cfg.tokenURL, AuthStyle: oauth2.AuthStyleInParams},
		RedirectURL:  h.cfg.redirectURI(),
	}
	tok, err := oc.Exchange(ctx, code, oauth2.VerifierOption(pc.verifier))
	if err != nil {
		h.finish(w, st, connectReasonExchangeFailed, describeExchangeError(err))
		return
	}
	if tok.RefreshToken == "" {
		h.finish(w, st, connectReasonNoRefreshToken, "")
		return
	}
	idToken, _ := tok.Extra("id_token").(string)
	if idToken == "" {
		h.finish(w, st, connectReasonExchangeFailed, "no id_token in the token response")
		return
	}
	verifier := &googleVerifier{clientID: h.cfg.clientID, tokeninfoURL: h.cfg.tokeninfoURL, hc: h.httpClient}
	accountEmail, err := verifier.Verify(r.WithContext(ctx), idToken)
	if err != nil {
		// Not err itself: a transport error quotes the tokeninfo URL, which
		// carries the ID token.
		h.finish(w, st, connectReasonExchangeFailed, "the ID token did not verify")
		return
	}
	granted, _ := tok.Extra("scope").(string)
	if missing := missingConnectScopes(granted); len(missing) > 0 {
		h.finishMissing(w, st, missing)
		return
	}
	if !h.stillOperator(st.Project, st.Email) {
		h.finish(w, st, connectReasonNotAllowed, "")
		return
	}

	nonce, ciphertext, err := h.cfg.sealer.Seal([]byte(tok.RefreshToken), connections.AccountAAD(st.Project, st.Account))
	if err != nil {
		h.finish(w, st, connectReasonStoreFailed, "sealing failed")
		return
	}
	cred := &agentdb.ConnectionCredential{
		Project:      st.Project,
		Account:      st.Account,
		Provider:     googleConnectProvider,
		AccountEmail: accountEmail,
		Scopes:       append([]string(nil), requiredProductScopes...),
		KeyID:        h.cfg.sealer.KeyID(),
		Nonce:        nonce,
		Ciphertext:   ciphertext,
		ConnectedBy:  st.Email,
		ConnectedAt:  h.now().UnixMilli(),
	}
	if err := h.store.PutConnectionCredential(r.Context(), cred); err != nil {
		// The store's errors carry no bytes: validation names fields, and a
		// database error does not quote bound parameters.
		h.finish(w, st, connectReasonStoreFailed, err.Error())
		return
	}
	if h.accounts != nil {
		h.accounts.Invalidate(st.Project, st.Account)
	}
	h.logf("[agentd] connect google: callback project=%q account=%q user=%q result=connected account_email=%q", st.Project, st.Account, st.Email, accountEmail)
	http.Redirect(w, r, h.settingsURL(st, url.Values{"result": {"connected"}}), http.StatusSeeOther)
}

// finish logs the outcome and redirects to the project's Settings with a
// reason code. detail is for the log only and must be safe to log.
func (h *googleConnect) finish(w http.ResponseWriter, st connectState, reason, detail string) {
	if detail != "" {
		detail = " (" + detail + ")"
	}
	h.logf("[agentd] connect google: callback project=%q account=%q user=%q result=error reason=%s%s", st.Project, st.Account, st.Email, reason, detail)
	w.Header().Set("Location", h.settingsURL(st, url.Values{"result": {"error"}, "reason": {reason}}))
	w.WriteHeader(http.StatusSeeOther)
}

// finishMissing is missing_scopes, which also says which (A3): short names
// such as "gmail.compose", comma-separated, in the `missing` parameter.
func (h *googleConnect) finishMissing(w http.ResponseWriter, st connectState, missing []string) {
	short := make([]string, len(missing))
	for i, s := range missing {
		short[i] = strings.TrimPrefix(s, "https://www.googleapis.com/auth/")
	}
	h.logf("[agentd] connect google: callback project=%q account=%q user=%q result=error reason=%s missing=%s", st.Project, st.Account, st.Email, connectReasonMissingScopes, strings.Join(short, ","))
	w.Header().Set("Location", h.settingsURL(st, url.Values{"result": {"error"}, "reason": {connectReasonMissingScopes}, "missing": {strings.Join(short, ",")}}))
	w.WriteHeader(http.StatusSeeOther)
}

// settingsURL is ${publicBase}/p/<project>/settings?connect=<account>&….
func (h *googleConnect) settingsURL(st connectState, extra url.Values) string {
	q := url.Values{"connect": {st.Account}}
	for k, v := range extra {
		q[k] = v
	}
	return strings.TrimRight(h.cfg.publicBase, "/") + "/p/" + url.PathEscape(st.Project) + "/settings?" + q.Encode()
}

func (h *googleConnect) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     googleConnectCookie,
		Value:    "",
		Path:     googleConnectCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie(),
		SameSite: http.SameSiteLaxMode,
	})
}

// invalidStatePage answers a state that does not verify: it cannot name a
// project, so there is nowhere to redirect to.
func (h *googleConnect) invalidStatePage(w http.ResponseWriter) {
	base := html.EscapeString(strings.TrimRight(h.cfg.publicBase, "/"))
	if base == "" {
		base = "/"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<meta charset="utf-8">
<title>Connect Google</title>
<p>This Connect Google link is not valid, or has been used already.</p>
<p><a href="%s">Return to Agent Bob</a> and press Connect Google again.</p>
`, base)
}

// missingConnectScopes returns the required product scopes absent from a
// token response's space-separated scope string, in requiredProductScopes
// order.
func missingConnectScopes(granted string) []string {
	have := map[string]bool{}
	for _, s := range strings.Fields(granted) {
		have[s] = true
	}
	var missing []string
	for _, s := range requiredProductScopes {
		if !have[s] {
			missing = append(missing, s)
		}
	}
	return missing
}

// describeExchangeError reduces an exchange failure to something safe to
// log: Google's error code and HTTP status, never the response body or the
// request (which carries the code and verifier).
func describeExchangeError(err error) string {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		status := 0
		if re.Response != nil {
			status = re.Response.StatusCode
		}
		if re.ErrorCode != "" {
			return fmt.Sprintf("token endpoint: %s, status %d", re.ErrorCode, status)
		}
		return fmt.Sprintf("token endpoint: status %d", status)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "token endpoint: timed out"
	}
	return "token endpoint: request failed"
}

// ── DELETE /agent/connections/{account} ───────────────────────────────────────

// disconnect revokes at Google first, best effort, then deletes the row
// through the config log whether or not the revoke worked (A12).
func (h *googleConnect) disconnect(w http.ResponseWriter, r *http.Request) {
	email, project, err := connectAuthority(r, h.jwtSecretSet)
	if err != nil {
		writeConnectError(w, http.StatusForbidden, err.Error())
		return
	}
	account := r.PathValue("account")
	notConnected := fmt.Sprintf("the Google account %q is not connected in this project", account)

	cred, err := h.store.GetConnectionCredential(r.Context(), project, account)
	if errors.Is(err, agentdb.ErrConnectionCredentialNotFound) {
		writeConnectError(w, http.StatusNotFound, notConnected)
		return
	}
	if err != nil {
		h.logf("[agentd] connect google: disconnect project=%q account=%q user=%q result=error: %v", project, account, email, err)
		writeConnectError(w, http.StatusInternalServerError, "could not read the stored connection")
		return
	}

	revoked, revokeDetail := h.revoke(r.Context(), cred)

	if err := h.store.DeleteConnectionCredential(r.Context(), project, account, email); err != nil {
		if errors.Is(err, agentdb.ErrConnectionCredentialNotFound) {
			writeConnectError(w, http.StatusNotFound, notConnected)
			return
		}
		h.logf("[agentd] connect google: disconnect project=%q account=%q user=%q result=error revoked=%t: %v", project, account, email, revoked, err)
		writeConnectError(w, http.StatusInternalServerError, "could not remove the stored connection")
		return
	}
	if h.accounts != nil {
		h.accounts.Invalidate(project, account)
	}
	h.logf("[agentd] connect google: disconnect project=%q account=%q user=%q result=disconnected revoked=%t%s", project, account, email, revoked, revokeDetail)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"revoked": revoked})
}

// revoke asks Google to revoke the stored refresh token. It reports false
// (with a loggable detail) when there is no usable key, the row was sealed
// under another key, it does not open, or Google does not answer 200.
func (h *googleConnect) revoke(ctx context.Context, cred *agentdb.ConnectionCredential) (bool, string) {
	if h.cfg.sealer == nil {
		return false, " (no key to open the stored token)"
	}
	if cred.KeyID != h.cfg.sealer.KeyID() {
		return false, " (sealed with another key)"
	}
	plain, err := h.cfg.sealer.Open(cred.Nonce, cred.Ciphertext, connections.AccountAAD(cred.Project, cred.Account))
	if err != nil {
		return false, " (stored token does not open)"
	}
	ctx, cancel := context.WithTimeout(ctx, googleConnectHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.revokeURL, strings.NewReader(url.Values{"token": {string(plain)}}.Encode()))
	if err != nil {
		return false, " (revoke request failed)"
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		// Not err: it would not quote the body, but keep one rule.
		return false, " (revoke request failed)"
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf(" (revoke: status %d)", resp.StatusCode)
	}
	return true, ""
}

// writeConnectError is the addendum's JSON error shape, {"error": "..."}.
func writeConnectError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
