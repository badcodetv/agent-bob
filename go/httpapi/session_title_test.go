package httpapi

// session_title_test.go — `title` on POST /agent/session. The onboarding
// interview sets one, because its first message is instructions for the
// interviewer and the session list otherwise read "Untitled".

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCreateSessionKeepsTheCallersTitle(t *testing.T) {
	h, store, _ := workerChatHandlers(t)

	rec := do(h, http.MethodPost, "/agent/session", `{"title":"  Onboarding interview  "}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	row := store.last()
	if row == nil {
		t.Fatal("no session row persisted")
	}
	if row.Title != "Onboarding interview" {
		t.Errorf("session.title = %q, want %q", row.Title, "Onboarding interview")
	}
}

func TestCreateSessionWithoutATitleLeavesItEmpty(t *testing.T) {
	h, store, _ := workerChatHandlers(t)

	if rec := do(h, http.MethodPost, "/agent/session", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	// Empty is what lets the title bot name an ordinary chat from its first
	// message, so no default may be invented here.
	if row := store.last(); row == nil || row.Title != "" {
		t.Errorf("session row = %+v, want an empty title", row)
	}
}

func TestSessionTitleCutsOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("é", 200) // 400 bytes
	got := sessionTitle(long)
	if len(got) > maxSessionTitleLen {
		t.Errorf("len = %d, want <= %d", len(got), maxSessionTitleLen)
	}
	if !utf8.ValidString(got) {
		t.Errorf("cut mid-rune: %q", got)
	}
	if got != strings.Repeat("é", 127) {
		t.Errorf("got %d runes, want 127", utf8.RuneCountInString(got))
	}
}
