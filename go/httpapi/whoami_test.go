package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// doWhoami mounts a fresh Handlers with the given identity and runs GET
// /agent/whoami through the real mux, the way the other route tests do.
func doWhoami(t *testing.T, id IdentityFunc) *httptest.ResponseRecorder {
	t.Helper()
	h := newHandlers(t, Config{Runner: stubRunner{}, Store: stubStore{}, Identity: id})
	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agent/whoami", nil))
	return rec
}

// TestWhoami pins the exact response shape onboarding-work-plan §1.1 asks for:
// {"email": "...", "project": "...", "operator": true|false}.
func TestWhoami(t *testing.T) {
	t.Run("non-operator identity", func(t *testing.T) {
		rec := doWhoami(t, func(*http.Request) (Identity, error) {
			return Identity{UserEmail: "kai@example.com", Customer: "acme", Operator: false}, nil
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		var got whoamiResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %s: %v", rec.Body, err)
		}
		want := whoamiResponse{Email: "kai@example.com", Project: "acme", Operator: false}
		if got != want {
			t.Fatalf("whoami = %+v, want %+v", got, want)
		}
	})

	t.Run("operator identity", func(t *testing.T) {
		rec := doWhoami(t, func(*http.Request) (Identity, error) {
			return Identity{UserEmail: "kai@example.com", Customer: "acme", Operator: true}, nil
		})
		got := decodeWhoami(t, rec)
		if !got.Operator {
			t.Fatalf("whoami = %+v, want operator=true", got)
		}
	})

	t.Run("unauthenticated is 401", func(t *testing.T) {
		rec := doWhoami(t, func(*http.Request) (Identity, error) { return Identity{}, errors.New("no token") })
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
	})
}

// The route is mounted at the canonical path and answers only GET.
func TestWhoamiRouteIsMounted(t *testing.T) {
	if DefaultEndpoints.Whoami != "GET /agent/whoami" {
		t.Fatalf("unexpected default route: %q", DefaultEndpoints.Whoami)
	}
	h := newHandlers(t, Config{Runner: stubRunner{}, Store: stubStore{}, Identity: okIdentity})
	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agent/whoami", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: want 405, got %d body=%s", rec.Code, rec.Body)
	}
}

func decodeWhoami(t *testing.T, rec *httptest.ResponseRecorder) whoamiResponse {
	t.Helper()
	var out whoamiResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return out
}
