package main

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Data under the store directory that is backed up, besides the database.
// Repository mirrors and Docker images are not: they can be fetched or built
// again. Match reports and logs are not either: they are large, they expire
// (see PruneFiles), and the results are in the database.
var backupDirs = []string{"builds"}
var backupFiles = []string{"teams.json", "registry-last.json"}

type BackupFile struct {
	Path     string    `json:"path"` // relative to the data directory, with forward slashes
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// BackupFiles lists the files to back up
func (s *Store) BackupFiles() ([]BackupFile, error) {
	var files []BackupFile
	add := func(path string, info fs.FileInfo) {
		if strings.HasSuffix(path, ".tmp") {
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

// GET /backup (with the admin password, see Web.admin): everything in one
// tar archive, named after the time of the backup: a consistent snapshot of
// the database, and all the files
func (web *Web) handleBackup(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	// The snapshot first: every match it has then has its files in the list
	snapshot, err := web.Store.SnapshotDatabase()
	if err != nil {
		serverError(w, err)
		return
	}
	defer os.Remove(snapshot)
	files, err := web.Store.BackupFiles()
	if err != nil {
		serverError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="ladder-backup-%s.tar"`, now.UTC().Format("2006-01-02T150405Z")))

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
