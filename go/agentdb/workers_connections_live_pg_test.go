package agentdb

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// TestLivePG_WorkerConnectionsRoundTrip is the live-Postgres half of T6's
// round-trip criterion: sqlite tolerates almost any column shape, so only a
// real jsonb column proves nil, [] and a mixed list (["github","*"]) come back
// exactly as written rather than being coerced into each other by a driver or
// a NOT NULL default. Runs migration 050 via Open, then round-trips through
// the real UpsertWorker/GetWorker path — not gorm.AutoMigrate.
func TestLivePG_WorkerConnectionsRoundTrip(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := "proj-" + uuid.New().String()

	tests := []struct {
		name string
		in   ConnectionList
		want ConnectionList
	}{
		{"nil stays null", nil, nil},
		{"empty list stays empty, non-nil", ConnectionList{}, ConnectionList{}},
		{"mixed list keeps order", ConnectionList{"github", "*"}, ConnectionList{"github", "*"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorker(project, "connected-"+uuid.New().String())
			w.Connections = tc.in
			if _, err := s.UpsertWorker(ctx, w, ConfigWrite{}); err != nil {
				t.Fatalf("upsert: %v", err)
			}

			read, err := s.GetWorker(ctx, w.Project, w.Name)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if (tc.want == nil) != (read.Connections == nil) {
				t.Fatalf("nil-ness lost: want nil=%v, got nil=%v (%#v)", tc.want == nil, read.Connections == nil, read.Connections)
			}
			if !reflect.DeepEqual(read.Connections, tc.want) {
				t.Fatalf("connections round-trip: want %#v, got %#v", tc.want, read.Connections)
			}

			// Confirm it really is jsonb, not text pretending to be jsonb.
			var typ sql.NullString
			if err := s.DB().Raw(
				"SELECT jsonb_typeof(connections) FROM workers WHERE project = ? AND name = ?",
				w.Project, w.Name,
			).Scan(&typ).Error; err != nil {
				t.Fatalf("jsonb_typeof: %v", err)
			}
			if tc.in == nil {
				if typ.Valid {
					t.Fatalf("nil connections should read back as SQL NULL (jsonb_typeof NULL), got %q", typ.String)
				}
			} else if !typ.Valid || typ.String != "array" {
				t.Fatalf("connections column is not a jsonb array: jsonb_typeof=%v", typ)
			}
		})
	}
}
