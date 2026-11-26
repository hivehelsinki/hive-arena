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
	config := &Config{Timezone: "UTC"}
	config.Web.AdminPassword = "secret"

	a, b := Bot{Name: "a"}, Bot{Name: "b"}
	match, _ := store.RecordMatch(MatchInfo{}, fakeResult("m", []Bot{a, b}, []uint{1, 0}, []BotStatus{Finished, Finished}))
	store.RecordBuild(&Build{Team: "a", Status: BuildOK}, "build log")
	store.SavePoolState(&PoolState{Teams: []TeamState{{Team: Team{Name: "a", ID: "a"}}}})

	web, err := NewWeb(config, store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(web.Handler())
	defer server.Close()

	// The admin password is needed (the old token in the URL is not enough)
	for _, password := range []string{"", "nope", "?token=secret"} {
		req, _ := http.NewRequest("GET", server.URL+"/backup", nil)
		if password == "?token=secret" {
			req, _ = http.NewRequest("GET", server.URL+"/backup"+password, nil)
		} else if password != "" {
			req.SetBasicAuth("admin", password)
		}
		res, _ := http.DefaultClient.Do(req)
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Errorf("%q: expected 401, got %d", password, res.StatusCode)
		}
	}

	req, _ := http.NewRequest("GET", server.URL+"/backup", nil)
	req.SetBasicAuth("admin", "secret")
	res, err := http.DefaultClient.Do(req)
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

	// Match reports and logs are not backed up
	for _, want := range []string{"ladder.db", "builds/1.log.gz", "teams.json"} {
		if files[want] == nil {
			t.Errorf("missing %s in the archive", want)
		}
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
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
	web, _ := NewWeb(&Config{Timezone: "UTC"}, store)
	server := httptest.NewServer(web.Handler())
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/backup", nil)
	req.SetBasicAuth("admin", "")
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("backups should be disabled without a password, got %d", res.StatusCode)
	}
}
