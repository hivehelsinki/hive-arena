package main

import (
	"archive/tar"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestBackup(t *testing.T) {
	store := newTestStore(t)
	config := &Config{Schedule: Schedule{Timezone: "UTC"}}
	config.Web.BackupToken = "secret"

	a, b := Bot{Name: "a"}, Bot{Name: "b"}
	run, _ := store.CreateRun("ladder", nil, []Bot{a, b})
	first, _ := store.RecordMatch(run, fakeResult("m", []Bot{a, b}, []uint{1, 0}, []BotStatus{Finished, Finished}))

	web, err := NewWeb(config, store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(web.Handler())
	defer server.Close()

	get := func(path string, status int) *http.Response {
		t.Helper()
		res, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
			t.Fatalf("%s: expected status %d, got %d", path, status, res.StatusCode)
		}
		return res
	}

	// Without the right token, the routes do not exist
	get("/backup/manifest", 404)
	get("/backup/manifest?token=nope", 404)
	get("/backup/archive.tar", 404)
	get("/backup/files/teams.json?token=nope", 404)

	// Manifest and single files
	res := get("/backup/manifest?token=secret", 200)
	var manifest struct {
		Time  int64
		Files []BackupFile
	}
	json.NewDecoder(res.Body).Decode(&manifest)
	res.Body.Close()
	if len(manifest.Files) != 3 { // report and two logs
		t.Errorf("unexpected manifest: %+v", manifest)
	}
	res = get("/backup/files/"+manifest.Files[0].Path+"?token=secret", 200)
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if int64(len(data)) != manifest.Files[0].Size {
		t.Errorf("unexpected file size %d, expected %d", len(data), manifest.Files[0].Size)
	}
	get("/backup/files/ladder.db?token=secret", 404)
	get("/backup/files/reports/../ladder.db?token=secret", 404)

	// Full archive: a database snapshot that can be opened, and all files
	readArchive := func(path string) (map[string][]byte, string) {
		t.Helper()
		res := get(path, 200)
		defer res.Body.Close()
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
		return files, res.Header.Get("X-Backup-Time")
	}

	files, backupTime := readArchive("/backup/archive.tar?token=secret")
	if len(files) != 4 || files["ladder.db"] == nil || files["reports/1.json.gz"] == nil {
		t.Fatalf("unexpected archive content: %d files", len(files))
	}

	restored := filepath.Join(t.TempDir(), "ladder.db")
	os.WriteFile(restored, files["ladder.db"], 0o644)
	db, err := sql.Open("sqlite", restored)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM matches WHERE id = ?`, first).Scan(&count); err != nil || count != 1 {
		t.Errorf("the snapshot should contain the match: %d, %v", count, err)
	}
	db.Close()

	// Incremental archive: only what changed since the previous backup
	since, _ := strconv.ParseInt(backupTime, 10, 64)
	time.Sleep(1100 * time.Millisecond)
	store.RecordMatch(run, fakeResult("m", []Bot{a, b}, []uint{0, 1}, []BotStatus{Finished, Finished}))

	files, _ = readArchive("/backup/archive.tar?token=secret&since=" + strconv.FormatInt(since+1, 10))
	if len(files) != 4 || files["reports/2.json.gz"] == nil || files["reports/1.json.gz"] != nil {
		names := []string{}
		for name := range files {
			names = append(names, name)
		}
		t.Errorf("expected the database and the new match's files, got %v", names)
	}

	// No snapshot left behind
	if leftovers, _ := filepath.Glob(filepath.Join(store.dir, "snapshot-*")); len(leftovers) != 0 {
		t.Errorf("snapshots left behind: %v", leftovers)
	}
}

func TestBackupDisabledWithoutToken(t *testing.T) {
	store := newTestStore(t)
	web, _ := NewWeb(&Config{Schedule: Schedule{Timezone: "UTC"}}, store)
	server := httptest.NewServer(web.Handler())
	defer server.Close()

	res, _ := http.Get(server.URL + "/backup/manifest?token=")
	if res.StatusCode != 404 {
		t.Errorf("backups should be disabled without a token, got %d", res.StatusCode)
	}
}
