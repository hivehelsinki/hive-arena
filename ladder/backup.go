package main

import (
	"archive/tar"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Data under the store directory that is backed up, besides the database.
// Repository mirrors and Docker images are not: they can be fetched or built again.
var backupDirs = []string{"reports", "logs", "builds"}
var backupFiles = []string{"teams.json"}

type BackupFile struct {
	Path     string    `json:"path"` // relative to the data directory, with forward slashes
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// BackupFiles lists the files to back up, modified at or after `since` (zero for all)
func (s *Store) BackupFiles(since time.Time) ([]BackupFile, error) {
	var files []BackupFile
	add := func(path string, info fs.FileInfo) {
		if info.ModTime().Before(since) || strings.HasSuffix(path, ".tmp") {
			return
		}
		rel, _ := filepath.Rel(s.dir, path)
		files = append(files, BackupFile{Path: filepath.ToSlash(rel), Size: info.Size(), Modified: info.ModTime()})
	}

	for _, dir := range backupDirs {
		err := filepath.Walk(filepath.Join(s.dir, dir), func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if !info.IsDir() {
				add(path, info)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for _, name := range backupFiles {
		path := filepath.Join(s.dir, name)
		if info, err := os.Stat(path); err == nil {
			add(path, info)
		}
	}
	return files, nil
}

// SnapshotDatabase writes a consistent copy of the database to a temporary
// file, while the ladder keeps running. The caller removes the file.
func (s *Store) SnapshotDatabase() (string, error) {
	tmp, err := os.CreateTemp(s.dir, "snapshot-*.db")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()
	os.Remove(path) // VACUUM INTO needs a file that does not exist

	if _, err := s.db.Exec(`VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// The backup routes, enabled when a token is configured. Everything in them
// is read-only.
func (web *Web) backupAllowed(w http.ResponseWriter, r *http.Request) bool {
	token := web.Config.Web.BackupToken
	given := r.URL.Query().Get("token")
	if token == "" || subtle.ConstantTimeCompare([]byte(given), []byte(token)) != 1 {
		http.NotFound(w, r)
		return false
	}
	return true
}

func parseSince(r *http.Request) (time.Time, error) {
	since := r.URL.Query().Get("since")
	if since == "" {
		return time.Time{}, nil
	}
	if seconds, err := strconv.ParseInt(since, 10, 64); err == nil {
		return time.Unix(seconds, 0), nil
	}
	return time.Parse(time.RFC3339, since)
}

// GET /backup/manifest?token=&since=: the files to back up, as JSON
func (web *Web) handleBackupManifest(w http.ResponseWriter, r *http.Request) {
	if !web.backupAllowed(w, r) {
		return
	}
	since, err := parseSince(r)
	if err != nil {
		http.Error(w, "invalid since: "+err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now()
	files, err := web.Store.BackupFiles(since)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"time": now.Unix(), "files": files})
}

// GET /backup/files/<path>?token=: one file of the manifest
func (web *Web) handleBackupFile(w http.ResponseWriter, r *http.Request) {
	if !web.backupAllowed(w, r) {
		return
	}
	rel := r.PathValue("path")
	allowed := false
	for _, prefix := range backupDirs {
		allowed = allowed || strings.HasPrefix(rel, prefix+"/")
	}
	for _, name := range backupFiles {
		allowed = allowed || rel == name
	}
	if !allowed || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, filepath.Join(web.Store.dir, filepath.FromSlash(rel)))
}

// GET /backup/database?token=: a consistent snapshot of the SQLite database
func (web *Web) handleBackupDatabase(w http.ResponseWriter, r *http.Request) {
	if !web.backupAllowed(w, r) {
		return
	}
	path, err := web.Store.SnapshotDatabase()
	if err != nil {
		serverError(w, err)
		return
	}
	defer os.Remove(path)

	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="ladder.db"`)
	http.ServeFile(w, r, path)
}

// GET /backup/archive.tar?token=&since=: everything in one tar archive, the
// database snapshot and the files modified since the given time (all of
// them by default). The X-Backup-Time header gives the time to pass as
// `since` next time, for an incremental backup.
func (web *Web) handleBackupArchive(w http.ResponseWriter, r *http.Request) {
	if !web.backupAllowed(w, r) {
		return
	}
	since, err := parseSince(r)
	if err != nil {
		http.Error(w, "invalid since: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Files written after this point go into the next backup
	now := time.Now()
	files, err := web.Store.BackupFiles(since)
	if err != nil {
		serverError(w, err)
		return
	}
	snapshot, err := web.Store.SnapshotDatabase()
	if err != nil {
		serverError(w, err)
		return
	}
	defer os.Remove(snapshot)

	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="ladder-backup-%s.tar"`, now.Format("20060102-150405")))
	w.Header().Set("X-Backup-Time", strconv.FormatInt(now.Unix(), 10))

	archive := tar.NewWriter(w)
	addFile := func(name string, path string) error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		header := &tar.Header{Name: name, Mode: 0o644, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		_, err = io.Copy(archive, file)
		return err
	}

	if err := addFile("ladder.db", snapshot); err != nil {
		log.Printf("Backup: %s", err)
		return
	}
	for _, f := range files {
		if err := addFile(f.Path, filepath.Join(web.Store.dir, filepath.FromSlash(f.Path))); err != nil {
			// The response has started: a broken archive tells the client something went wrong
			log.Printf("Backup: %s", err)
			return
		}
	}
	if err := archive.Close(); err != nil {
		log.Printf("Backup: %s", err)
	}
}
