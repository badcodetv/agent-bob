package agentdb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// design/2026-09-11-project-connections.md, addendum T17. The bytes below are
// stand-ins, never a real credential: the tests only need values distinctive
// enough that finding them anywhere in a config-event payload is unambiguous.
var (
	testCredNonce      = []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	testCredCiphertext = []byte("sealed-bytes-that-must-never-reach-the-log")
	testCredKeyID      = "0123456789abcdef"
)

func testCredential(project, account string) *ConnectionCredential {
	return &ConnectionCredential{
		Project:      project,
		Account:      account,
		Provider:     "google",
		AccountEmail: "enc-office@example.com",
		Scopes:       []string{"openid", "email", "https://www.googleapis.com/auth/drive"},
		KeyID:        testCredKeyID,
		Nonce:        append([]byte(nil), testCredNonce...),
		Ciphertext:   append([]byte(nil), testCredCiphertext...),
		ConnectedBy:  "operator@example.com",
		ConnectedAt:  1_789_000_000_000,
	}
}

// assertNoSecretMaterial fails if any form of the sealed bytes or the key id
// appears in a config event. The message deliberately names WHAT leaked, never
// the bytes themselves, so a failing run prints nothing worth protecting.
func assertNoSecretMaterial(t *testing.T, ev *ConfigEvent) {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal config event: %v", err)
	}
	forbidden := map[string][]byte{
		"raw ciphertext":       testCredCiphertext,
		"base64 ciphertext":    []byte(base64.StdEncoding.EncodeToString(testCredCiphertext)),
		"base64url ciphertext": []byte(base64.RawURLEncoding.EncodeToString(testCredCiphertext)),
		"hex ciphertext":       []byte(hex.EncodeToString(testCredCiphertext)),
		"raw nonce":            testCredNonce,
		"base64 nonce":         []byte(base64.StdEncoding.EncodeToString(testCredNonce)),
		"base64url nonce":      []byte(base64.RawURLEncoding.EncodeToString(testCredNonce)),
		"hex nonce":            []byte(hex.EncodeToString(testCredNonce)),
		"key id":               []byte(testCredKeyID),
	}
	for what, needle := range forbidden {
		if bytes.Contains(raw, needle) {
			t.Fatalf("%s event payload contains the %s", ev.Action, what)
		}
	}
	for _, field := range []string{"nonce", "ciphertext", "key_id"} {
		if _, ok := ev.Payload[field]; ok {
			t.Fatalf("%s event payload has a %q field", ev.Action, field)
		}
	}
}

func newConnectionCredentialStore(t *testing.T) *Store {
	t.Helper()
	s := newConfigLogTestStore(t)
	if err := InstallConfigEventGuard(s.gdb); err != nil {
		t.Fatalf("install guard: %v", err)
	}
	return s
}

// connectionCredentialStores runs a test against sqlite always and against a
// live Postgres when AGENTKIT_TEST_POSTGRES_URL is set. The live half is the
// one that proves the real migration: BYTEA and JSONB round trips, NOT NULL.
func connectionCredentialStores(t *testing.T, fn func(t *testing.T, s *Store, project string)) {
	t.Run("sqlite", func(t *testing.T) {
		fn(t, newConnectionCredentialStore(t), "enc")
	})
	t.Run("live_postgres", func(t *testing.T) {
		s := openLivePG(t)
		project := "proj-" + uuid.New().String()
		t.Cleanup(func() {
			ctx := context.Background()
			_ = s.DB().WithContext(ctx).Exec("DELETE FROM connection_credentials WHERE project = ?", project).Error
			_ = s.PurgeConfigEvents(ctx, project)
		})
		fn(t, s, project)
	})
}

func TestConnectionCredential_RoundTripPreservesEveryField(t *testing.T) {
	connectionCredentialStores(t, func(t *testing.T, s *Store, project string) {
		ctx := context.Background()
		want := testCredential(project, "google")
		if err := s.PutConnectionCredential(ctx, want); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.GetConnectionCredential(ctx, project, "google")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !reflect.DeepEqual(got, testCredential(project, "google")) {
			t.Fatalf("round trip changed the row (field-by-field comparison failed; bytes elided)\n"+
				"project=%q account=%q provider=%q email=%q scopes=%v by=%q at=%d nonceLen=%d cipherLen=%d",
				got.Project, got.Account, got.Provider, got.AccountEmail, got.Scopes,
				got.ConnectedBy, got.ConnectedAt, len(got.Nonce), len(got.Ciphertext))
		}

		list, err := s.ListConnectionCredentials(ctx, project)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 1 || !reflect.DeepEqual(list[0], got) {
			t.Fatalf("list: want the one row, got %d rows", len(list))
		}
	})
}

func TestConnectionCredential_PutTwiceReplacesAndLogsTwice(t *testing.T) {
	connectionCredentialStores(t, func(t *testing.T, s *Store, project string) {
		ctx := context.Background()
		first := testCredential(project, "google")
		if err := s.PutConnectionCredential(ctx, first); err != nil {
			t.Fatalf("first put: %v", err)
		}
		second := testCredential(project, "google")
		second.AccountEmail = "enc-new@example.com"
		second.Ciphertext = []byte("a different sealed credential")
		second.ConnectedAt = first.ConnectedAt + 1000
		if err := s.PutConnectionCredential(ctx, second); err != nil {
			t.Fatalf("reconnect put: %v", err)
		}

		list, err := s.ListConnectionCredentials(ctx, project)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("a reconnect must replace, not add: got %d rows", len(list))
		}
		if list[0].AccountEmail != "enc-new@example.com" || list[0].ConnectedAt != second.ConnectedAt ||
			!bytes.Equal(list[0].Ciphertext, second.Ciphertext) {
			t.Fatalf("the reconnect did not replace the row's contents")
		}

		evs, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: project})
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		if len(evs) != 2 {
			t.Fatalf("want two config events, got %d", len(evs))
		}
		for _, ev := range evs {
			if ev.Action != ActionConnectionConnect {
				t.Fatalf("want %q, got %q", ActionConnectionConnect, ev.Action)
			}
		}
	})
}

func TestConnectionCredential_DeleteLogsOnceThenNotFound(t *testing.T) {
	connectionCredentialStores(t, func(t *testing.T, s *Store, project string) {
		ctx := context.Background()
		if err := s.PutConnectionCredential(ctx, testCredential(project, "google")); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := s.DeleteConnectionCredential(ctx, project, "google", "operator@example.com"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := s.GetConnectionCredential(ctx, project, "google"); !errors.Is(err, ErrConnectionCredentialNotFound) {
			t.Fatalf("get after delete: want ErrConnectionCredentialNotFound, got %v", err)
		}

		err := s.DeleteConnectionCredential(ctx, project, "google", "operator@example.com")
		if !errors.Is(err, ErrConnectionCredentialNotFound) {
			t.Fatalf("second delete: want ErrConnectionCredentialNotFound, got %v", err)
		}

		evs, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: project, Action: ActionConnectionDisconnect})
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		if len(evs) != 1 {
			t.Fatalf("want exactly one %s event (the second delete writes nothing), got %d",
				ActionConnectionDisconnect, len(evs))
		}
		if got, _ := evs[0].PayloadString("disconnected_by"); got != "operator@example.com" {
			t.Fatalf("disconnected_by: got %q", got)
		}
	})
}

// A9: the payload is metadata only, from an explicit struct. This is the test
// that fails if someone ever "simplifies" the payload to the row.
func TestConnectionCredential_EventPayloadsCarryMetadataOnly(t *testing.T) {
	connectionCredentialStores(t, func(t *testing.T, s *Store, project string) {
		ctx := context.Background()
		c := testCredential(project, "google")
		if err := s.PutConnectionCredential(ctx, c); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := s.DeleteConnectionCredential(ctx, project, "google", "remover@example.com"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		evs, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: project})
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		if len(evs) != 2 {
			t.Fatalf("want two events, got %d", len(evs))
		}

		wantKeys := map[string][]string{
			ActionConnectionConnect:    {"account", "account_email", "connected_at", "connected_by", "provider", "scopes"},
			ActionConnectionDisconnect: {"account", "account_email", "connected_at", "connected_by", "disconnected_by", "provider", "scopes"},
		}
		wantRationale := map[string]string{
			ActionConnectionConnect:    "Google account connected from the console",
			ActionConnectionDisconnect: "Google account disconnected from the console",
		}
		for _, ev := range evs {
			assertNoSecretMaterial(t, ev)

			var keys []string
			for k := range ev.Payload {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if !reflect.DeepEqual(keys, wantKeys[ev.Action]) {
				t.Fatalf("%s payload keys: want %v, got %v", ev.Action, wantKeys[ev.Action], keys)
			}
			if ev.Rationale != wantRationale[ev.Action] {
				t.Fatalf("%s rationale: got %q", ev.Action, ev.Rationale)
			}
			if strings.Contains(ev.Rationale, "@") {
				t.Fatalf("%s rationale carries an email address", ev.Action)
			}
			if got, _ := ev.PayloadString("account_email"); got != c.AccountEmail {
				t.Fatalf("%s account_email: got %q", ev.Action, got)
			}
			if got, _ := ev.PayloadString("connected_by"); got != c.ConnectedBy {
				t.Fatalf("%s connected_by: got %q", ev.Action, got)
			}
		}
	})
}

func TestConnectionCredential_EventsKeyToTheAccountAndAreNotRevertable(t *testing.T) {
	connectionCredentialStores(t, func(t *testing.T, s *Store, project string) {
		ctx := context.Background()
		if err := s.PutConnectionCredential(ctx, testCredential(project, "google")); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := s.DeleteConnectionCredential(ctx, project, "google", "operator@example.com"); err != nil {
			t.Fatalf("delete: %v", err)
		}

		evs, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: project, Entity: "connection:google"})
		if err != nil {
			t.Fatalf("list by entity: %v", err)
		}
		if len(evs) != 2 {
			t.Fatalf("entity connection:google: want both events, got %d", len(evs))
		}
		for _, ev := range evs {
			ref, err := EntityRefFor(ev)
			if err != nil {
				t.Fatalf("EntityRefFor(%s): %v", ev.Action, err)
			}
			if ref.String() != "connection:google" {
				t.Fatalf("EntityRefFor(%s): want connection:google, got %s", ev.Action, ref)
			}
		}

		// The kind rule is checked before the newest-change rule, so BOTH events
		// must be refused for the reason that matters, not just the older one
		// for being older.
		for _, ev := range evs {
			_, err := s.RevertEvent(ctx, project, ev.ID, ConfigWrite{})
			if !errors.Is(err, ErrRevertRefused) {
				t.Fatalf("revert of %s: want ErrRevertRefused, got %v", ev.Action, err)
			}
			if !strings.Contains(err.Error(), "nothing to put back") {
				t.Fatalf("revert of %s: refusal does not explain itself: %v", ev.Action, err)
			}
		}
		after, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: project})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(after) != 2 {
			t.Fatalf("a refused revert wrote something: %d events", len(after))
		}
	})
}

func TestConnectionCredential_ProjectIsolation(t *testing.T) {
	s := newConnectionCredentialStore(t)
	ctx := context.Background()
	if err := s.PutConnectionCredential(ctx, testCredential("enc", "google")); err != nil {
		t.Fatalf("put enc: %v", err)
	}
	if _, err := s.GetConnectionCredential(ctx, "globex", "google"); !errors.Is(err, ErrConnectionCredentialNotFound) {
		t.Fatalf("another project's credential must not be found: %v", err)
	}
	if err := s.DeleteConnectionCredential(ctx, "globex", "google", "x@example.com"); !errors.Is(err, ErrConnectionCredentialNotFound) {
		t.Fatalf("another project's credential must not be deletable: %v", err)
	}
	list, err := s.ListConnectionCredentials(ctx, "globex")
	if err != nil {
		t.Fatalf("list globex: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("globex list: want an empty, non-nil slice, got %d rows (nil=%v)", len(list), list == nil)
	}
	if _, err := s.ListConnectionCredentials(ctx, " "); err == nil {
		t.Fatalf("a blank project must be refused (P5)")
	}
}

func TestConnectionCredential_PutValidatesBeforeWriting(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *ConnectionCredential)
		wantErr string
	}{
		{"nil", nil, "is required"},
		{"blank project", func(c *ConnectionCredential) { c.Project = " " }, "project"},
		{"blank account", func(c *ConnectionCredential) { c.Account = "" }, "account"},
		{"bad account name", func(c *ConnectionCredential) { c.Account = "Google Account" }, "account"},
		{"blank provider", func(c *ConnectionCredential) { c.Provider = "" }, "provider"},
		{"blank email", func(c *ConnectionCredential) { c.AccountEmail = "" }, "account_email"},
		{"blank key id", func(c *ConnectionCredential) { c.KeyID = "" }, "key_id"},
		{"empty nonce", func(c *ConnectionCredential) { c.Nonce = nil }, "nonce"},
		{"empty ciphertext", func(c *ConnectionCredential) { c.Ciphertext = nil }, "ciphertext"},
		{"blank connected_by", func(c *ConnectionCredential) { c.ConnectedBy = "" }, "connected_by"},
		{"zero connected_at", func(c *ConnectionCredential) { c.ConnectedAt = 0 }, "connected_at"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newConnectionCredentialStore(t)
			ctx := context.Background()
			var c *ConnectionCredential
			if tc.mutate != nil {
				c = testCredential("enc", "google")
				tc.mutate(c)
			}
			err := s.PutConnectionCredential(ctx, c)
			if !errors.Is(err, ErrConnectionCredentialInvalid) {
				t.Fatalf("want ErrConnectionCredentialInvalid, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error mentioning %q, got %v", tc.wantErr, err)
			}
			evs, _ := s.ListConfigEvents(ctx, ConfigEventQuery{Project: "enc"})
			if len(evs) != 0 {
				t.Fatalf("a refused put wrote %d config events", len(evs))
			}
		})
	}
}

// A nil scope list is stored as [] rather than JSON null: the column is
// NOT NULL DEFAULT '[]', and a reader should never have to tell the two apart.
func TestConnectionCredential_NilScopesReadBackEmpty(t *testing.T) {
	connectionCredentialStores(t, func(t *testing.T, s *Store, project string) {
		ctx := context.Background()
		c := testCredential(project, "google")
		c.Scopes = nil
		if err := s.PutConnectionCredential(ctx, c); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.GetConnectionCredential(ctx, project, "google")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Scopes == nil || len(got.Scopes) != 0 {
			t.Fatalf("want an empty non-nil scope list, got %v (nil=%v)", got.Scopes, got.Scopes == nil)
		}
	})
}

// The bytes are `json:"-"` so a handler that encodes a row by accident still
// publishes no sealed material.
func TestConnectionCredential_JSONOmitsSealedMaterial(t *testing.T) {
	raw, err := json.Marshal(testCredential("enc", "google"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ev := &ConfigEvent{Action: "row-json"}
	if err := json.Unmarshal(raw, &ev.Payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertNoSecretMaterial(t, ev)
}

func TestLivePG_ConnectionCredentialsSchema052(t *testing.T) {
	s := openLivePG(t)
	var cols []struct {
		ColumnName string
		DataType   string
		IsNullable string
	}
	if err := s.DB().Raw(`SELECT column_name, data_type, is_nullable FROM information_schema.columns
		WHERE table_name = 'connection_credentials'`).Scan(&cols).Error; err != nil {
		t.Fatalf("read columns: %v", err)
	}
	want := map[string]string{
		"project": "text", "account": "text", "provider": "text", "account_email": "text",
		"scopes": "jsonb", "key_id": "text", "nonce": "bytea", "ciphertext": "bytea",
		"connected_by": "text", "connected_at": "bigint",
	}
	got := map[string]string{}
	for _, c := range cols {
		got[c.ColumnName] = c.DataType
		if c.IsNullable != "NO" {
			t.Fatalf("connection_credentials.%s must be NOT NULL", c.ColumnName)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("columns: want %v, got %v", want, got)
	}
}
