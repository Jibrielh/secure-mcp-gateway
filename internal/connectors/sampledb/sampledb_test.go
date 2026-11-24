package sampledb

import (
	"path/filepath"
	"testing"
)

func TestReadOnlySampleDatabase(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "sample.db")
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	regions, err := db.ListRegions(t.Context())
	if err != nil || len(regions) != 3 {
		t.Fatal(err, regions)
	}
	rows, err := db.RevenueByRegion(t.Context(), "North")
	if err != nil || len(rows) != 1 || rows[0].RevenueCents != 20000 {
		t.Fatal(err, rows)
	}
	if err := db.WriteRejected(t.Context()); err != nil {
		t.Fatal(err)
	}
}
