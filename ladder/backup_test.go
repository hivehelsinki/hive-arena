package main

import (
	"archive/tar"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestBackup(t *testing.T) {
	store := newTestStore(t)
	config := &Config{Schedule: Schedule{Timezone: "UTC"}}
	config.Web.BackupToken = "secret"

	a, b := Bot{Name: "a"}, Bot{Name: "b"}
	run, _ := store.CreateRun("ladder", nil, []Bot{a, b})
	match, _ := store.RecordMatch(run, fakeResult("m", []Bot{a, b}, []uint{1, 0}, []BotStatus{Finished, Finished}))
	store.RecordBuild(&Build{Team: "a", Status: BuildOK}, "build log")
	store.SavePoolState(&PoolState{Teams: []TeamState{{Team: Team{Name: "a"}}}})

	web, err := NewWeb(config, store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(web.Handler())
	defer server.Close()

	// Without the right token, the route does not exist
	for _, path := range []string{"/backup", "/backup?token=nope"} {
		res, _ := http.Get(server.URL + path)
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("%s: expected 404, got %d", path, res.StatusCode)
		}
	}

	res, err := http.Get(server.URL + "/backup?token=secret")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("backup failed: %v %v", res.StatusCode, err)
	}
	defer res.Body.Close()

	name := regexp.MustCompile(`filename="ladder-backup-\d{4}-\d{2}-\d{2}T\d{6}Z\.tar"`)
	if !name.MatchString(res.Header.Get("Content-Disposition")) {
		t.Errorf("the file name should contain the time: %s", res.Header.Get("Content-Disposition"))
	}

	files := make(map[string][]byte)
	archive := tar.NewReader(res.Body)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name], _ = io.ReadAll(archive)
	}

	for _, want := range []string{"ladder.db", "reports/1.json.gz", "logs/1/a.log.gz", "logs/1/b.log.gz", "builds/1.log.gz", "teams.json"} {
		if files[want] == nil {
			t.Errorf("missing %s in the archive", want)
		}
	}
	if len(files) != 6 {
		t.Errorf("expected 6 files, got %d", len(files))
	}

	// The snapshot is a working database
	restored := filepath.Join(t.TempDir(), "ladder.db")
	os.WriteFile(restored, files["ladder.db"], 0o644)
	db, err := sql.Open("sqlite", restored)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM matches WHERE id = ?`, match).Scan(&count); err != nil || count != 1 {
		t.Errorf("the snapshot should contain the match: %d, %v", count, err)
	}

	if leftovers, _ := filepath.Glob(filepath.Join(store.dir, "snapshot-*")); len(leftovers) != 0 {
		t.Errorf("snapshots left behind: %v", leftovers)
	}
}

func TestBackupDisabledWithoutToken(t *testing.T) {
	store := newTestStore(t)
	web, _ := NewWeb(&Config{Schedule: Schedule{Timezone: "UTC"}}, store)
	server := httptest.NewServer(web.Handler())
	defer server.Close()

	res, _ := http.Get(server.URL + "/backup?token=")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("backups should be disabled without a token, got %d", res.StatusCode)
	}
}
