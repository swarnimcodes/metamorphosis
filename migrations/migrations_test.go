package migrations_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
	"testing"

	"github.com/smarniw/metamorphosis/migrations"
)

// goose does not checksum applied migrations, so an edit to a file that already ran in production would go
// unnoticed and the schema would drift from the files. migrations.sum records every file's hash: a changed line
// in it shows in review, and CI fails until the file list and the sums agree. A new migration appends a line.
// Regenerate with `make migrations-sum` ONLY after adding a file, never to bless an edit.

func TestMigrationFilesMatchTheirRecordedHashes(t *testing.T) {
	recorded := map[string]string{}
	f, err := migrations.FS.Open("migrations.sum")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			t.Fatalf("malformed line in migrations.sum: %q", sc.Text())
		}
		recorded[name] = sum
	}

	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(body)
		want, ok := recorded[name]
		switch {
		case !ok:
			t.Errorf("%s is not in migrations.sum: run `make migrations-sum` after adding a migration", name)
		case want != hex.EncodeToString(got[:]):
			t.Errorf("%s changed after it was recorded. Applied migrations must never be edited: add a new migration instead", name)
		}
		delete(recorded, name)
		if !strings.HasPrefix(string(body), "-- +goose Up\n") {
			t.Errorf("%s must start with `-- +goose Up`", name)
		}
	}
	for name := range recorded {
		t.Errorf("migrations.sum lists %s, which no longer exists: applied migrations must not be deleted", name)
	}
}
