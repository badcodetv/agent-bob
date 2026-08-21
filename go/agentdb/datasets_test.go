package agentdb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Dataset store — the dialect-free half (O2).
//
// Every test here runs on the sqlite unit store (artifacts_test.go's
// newTestStore), which is only possible because CreateDatasetVersion validates
// its arguments BEFORE it checks the dialect: "this dataset name is illegal" is
// a more useful answer than "this deployment is not Postgres", and it is what
// makes the rejection cases provable without a live instance. Everything that
// touches the table lives in datasets_live_test.go.
//
// Naming: every test this ticket adds is TestDataset…, and every test needing a
// real Postgres is TestDatasetLivePG_… . agentdb carries four incompatible
// live-test naming shapes already; adopting any of them would make
// `-run 'TestDataset'` match nothing and the run would print `ok` while proving
// nothing.

// validDataset is the minimum a caller must supply: everything else the store
// fills in.
func validDataset() *Dataset {
	return &Dataset{
		Project:  "wolf",
		Name:     "1a2b3c4d-drone-suppliers-basket",
		BlobPath: testDatasetBlobPath,
		SHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}
}

// testDatasetBlobPath stands in for the key O6a will write. O2 knows nothing
// about the blob namespace — O3 owns DatasetBlobPrefix — so the tests use a
// literal rather than referring to a constant this ticket must not declare.
const testDatasetBlobPath = "_datasets/bytes/6f1c2c3e-1111-2222-3333-444455556666"

// TestDatasetCreateVersionRejectsBadArguments: one row per rejection the ticket
// enumerates. Each must fail BEFORE any database round-trip, which is proved by
// the error NOT being ErrDatasetRequiresPostgres — the store is sqlite here, so
// anything reaching the dialect guard would say so.
func TestDatasetCreateVersionRejectsBadArguments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name      string
		mutate    func(d *Dataset)
		ifVersion int
		wantErrIn string
	}{
		{"empty project", func(d *Dataset) { d.Project = "" }, 0, "project"},
		{"empty name", func(d *Dataset) { d.Name = "" }, 0, "name"},
		{"name outside the label-value charset", func(d *Dataset) { d.Name = "drone suppliers/basket" }, 0, "name"},
		{"name over 63 chars", func(d *Dataset) { d.Name = strings.Repeat("a", 64) }, 0, "name"},
		{"invalid label key", func(d *Dataset) { d.Labels = LabelSet{"bad key": "v"} }, 0, "label"},
		{"invalid label value", func(d *Dataset) { d.Labels = LabelSet{"hypothesis": "a b"} }, 0, "label"},
		{"empty sha256", func(d *Dataset) { d.SHA256 = "" }, 0, "sha256"},
		{"empty blob path", func(d *Dataset) { d.BlobPath = "" }, 0, "blob path"},
		{"negative size", func(d *Dataset) { d.SizeBytes = -1 }, 0, "size"},
		{"negative row count", func(d *Dataset) { d.RowCount = -1 }, 0, "row count"},
		{"negative ifVersion", func(d *Dataset) {}, -1, "if_version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validDataset()
			tc.mutate(d)
			got, err := s.CreateDatasetVersion(ctx, d, tc.ifVersion)
			if err == nil {
				t.Fatalf("expected rejection, got %+v", got)
			}
			if errors.Is(err, ErrDatasetRequiresPostgres) {
				t.Fatalf("argument validation must run BEFORE the dialect check, got %v", err)
			}
			var conflict ErrDatasetVersionConflict
			if errors.As(err, &conflict) {
				t.Fatalf("an argument error is never a conflict, got %v", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantErrIn) {
				t.Fatalf("error %q does not name %q", err, tc.wantErrIn)
			}
		})
	}

	// A nil dataset is an argument error too, not a panic.
	if _, err := s.CreateDatasetVersion(ctx, nil, 0); err == nil {
		t.Fatalf("nil dataset must be rejected")
	}
}

// TestDatasetMethodsRequirePostgres: past validation, every method guards the
// dialect and returns the DATASET sentinel — not the memory one, so a 501 on a
// dataset route does not blame memories.
func TestDatasetMethodsRequirePostgres(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateDatasetVersion(ctx, validDataset(), 0); !errors.Is(err, ErrDatasetRequiresPostgres) {
		t.Fatalf("CreateDatasetVersion: want ErrDatasetRequiresPostgres, got %v", err)
	}
	if _, err := s.CurrentDataset(ctx, "wolf", "some-name"); !errors.Is(err, ErrDatasetRequiresPostgres) {
		t.Fatalf("CurrentDataset: want ErrDatasetRequiresPostgres, got %v", err)
	}
	if _, err := s.GetDatasetVersion(ctx, "wolf", "some-name", 1); !errors.Is(err, ErrDatasetRequiresPostgres) {
		t.Fatalf("GetDatasetVersion: want ErrDatasetRequiresPostgres, got %v", err)
	}
	if errors.Is(ErrDatasetRequiresPostgres, ErrMemoryRequiresPostgres) {
		t.Fatalf("the dataset sentinel must not be the memory one")
	}
}

// TestDatasetReadsRejectBadArguments: the reads validate before the dialect
// guard too, so the same rules hold on either backend.
func TestDatasetReadsRejectBadArguments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CurrentDataset(ctx, "", "n"); err == nil || errors.Is(err, ErrDatasetRequiresPostgres) {
		t.Fatalf("empty project must be an argument error, got %v", err)
	}
	if _, err := s.CurrentDataset(ctx, "wolf", "bad name"); err == nil || errors.Is(err, ErrDatasetRequiresPostgres) {
		t.Fatalf("illegal name must be an argument error, got %v", err)
	}
	if _, err := s.GetDatasetVersion(ctx, "wolf", "n", 0); err == nil || errors.Is(err, ErrDatasetRequiresPostgres) {
		t.Fatalf("version 0 must be an argument error (versions start at 1), got %v", err)
	}
	if _, err := s.GetDatasetVersion(ctx, "wolf", "n", -3); err == nil || errors.Is(err, ErrDatasetRequiresPostgres) {
		t.Fatalf("negative version must be an argument error, got %v", err)
	}
}

// TestDatasetVersionConflictIsAValueCarryingCurrent: O1 gave Error() a VALUE
// receiver, so the store returns the struct by value and errors.As must find it
// as a value. A caller that cannot read Current cannot retry.
func TestDatasetVersionConflictIsAValueCarryingCurrent(t *testing.T) {
	var err error = ErrDatasetVersionConflict{Current: 7}

	var conflict ErrDatasetVersionConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("errors.As must find the conflict as a value")
	}
	if conflict.Current != 7 {
		t.Fatalf("Current = %d, want 7", conflict.Current)
	}
	if !strings.Contains(err.Error(), "7") {
		t.Fatalf("Error() must name the current version, got %q", err.Error())
	}
	// Current == 0 is the "name does not exist in this project" case, and it is
	// a legal conflict rather than an absence of one.
	zero := ErrDatasetVersionConflict{}
	if !strings.Contains(zero.Error(), "0") {
		t.Fatalf("zero conflict must still report a version, got %q", zero.Error())
	}
}
