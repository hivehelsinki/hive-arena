package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// The store keeps everything the ladder has played: an SQLite database for
// runs, matches and results, and compressed files for reports and logs.
type Store struct {
	db  *sql.DB
	dir string
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id INTEGER PRIMARY KEY,
	kind TEXT NOT NULL,          -- ladder, tournament, manual
	started_at TIMESTAMP NOT NULL,
	ended_at TIMESTAMP,
	status TEXT NOT NULL,        -- running, complete, incomplete, failed
	config TEXT NOT NULL         -- JSON: maps, repeats... as published
);

CREATE TABLE IF NOT EXISTS run_bots (
	run_id INTEGER NOT NULL REFERENCES runs(id),
	team TEXT NOT NULL,
	image TEXT NOT NULL,
	commit_id TEXT NOT NULL,
	PRIMARY KEY (run_id, team)
);

CREATE TABLE IF NOT EXISTS matches (
	id INTEGER PRIMARY KEY,
	run_id INTEGER REFERENCES runs(id),
	game_id TEXT NOT NULL,
	map TEXT NOT NULL,
	players INTEGER NOT NULL,
	started_at TIMESTAMP NOT NULL,
	ended_at TIMESTAMP NOT NULL,
	turns INTEGER NOT NULL,
	void INTEGER NOT NULL DEFAULT 0, -- not counted (e.g. the server failed)
	error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS matches_run ON matches(run_id);

CREATE TABLE IF NOT EXISTS match_bots (
	match_id INTEGER NOT NULL REFERENCES matches(id),
	team TEXT NOT NULL,
	image TEXT NOT NULL,
	commit_id TEXT NOT NULL,
	seat INTEGER NOT NULL,
	flowers INTEGER NOT NULL,
	points INTEGER NOT NULL,
	status TEXT NOT NULL,    -- finished, crashed, no-join
	exit_code INTEGER NOT NULL,
	PRIMARY KEY (match_id, team)
);
CREATE INDEX IF NOT EXISTS match_bots_team ON match_bots(team);
`

func OpenStore(dir string) (*Store, error) {
	for _, sub := range []string{"reports", "logs", "builds", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	// Left by an interrupted backup or match. Recent ones may belong to
	// another ladder process using the same data (e.g. the service).
	removeOld(filepath.Join(dir, "snapshot-*.db"), time.Hour)
	removeOld(filepath.Join(dir, "tmp", "*"), 24*time.Hour)

	// synchronous(FULL): a committed transaction survives a power loss
	db, err := sql.Open("sqlite", filepath.Join(dir, "ladder.db")+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, dir: dir}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// Schema versions: migrations[i] brings the database from version i to i+1.
// Existing migrations must never change: add new ones at the end.
var migrations = []string{
	schema + buildsSchema,
	`ALTER TABLE runs ADD COLUMN server_image TEXT NOT NULL DEFAULT '';
	ALTER TABLE runs ADD COLUMN ladder_revision TEXT NOT NULL DEFAULT '';
	CREATE INDEX IF NOT EXISTS matches_map ON matches(map);`,
	// The continuous ladder: matches outside of runs, with what played them,
	// and files that expire
	`ALTER TABLE matches ADD COLUMN kind TEXT NOT NULL DEFAULT 'ladder';
	ALTER TABLE matches ADD COLUMN server_image TEXT NOT NULL DEFAULT '';
	ALTER TABLE matches ADD COLUMN ladder_revision TEXT NOT NULL DEFAULT '';
	ALTER TABLE matches ADD COLUMN files_deleted INTEGER NOT NULL DEFAULT 0;
	UPDATE matches SET
		kind = COALESCE((SELECT CASE r.kind WHEN 'tournament' THEN 'tournament' ELSE 'ladder' END FROM runs r WHERE r.id = matches.run_id), 'manual'),
		server_image = COALESCE((SELECT r.server_image FROM runs r WHERE r.id = matches.run_id), ''),
		ladder_revision = COALESCE((SELECT r.ladder_revision FROM runs r WHERE r.id = matches.run_id), '');
	CREATE INDEX IF NOT EXISTS matches_ended ON matches(ended_at);
	CREATE INDEX IF NOT EXISTS match_bots_team_match ON match_bots(team, match_id);`,
}

// migrate brings the database schema to the current version. A database
// with data is first copied, in case a migration goes wrong.
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("the database is from a newer version of the ladder (schema version %d, this one knows %d)", version, len(migrations))
	}
	if version == len(migrations) {
		return nil
	}

	var tables int
	s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables)
	if tables > 0 {
		backup := filepath.Join(s.dir, fmt.Sprintf("ladder-v%d-%s.db", version, time.Now().UTC().Format("20060102T150405Z")))
		if _, err := s.db.Exec(`VACUUM INTO ?`, backup); err != nil {
			return fmt.Errorf("copying the database before migrating it: %w", err)
		}
		log.Printf("Database copied to %s before migrating it", backup)
	}

	for v := version; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrating the database to version %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if tables > 0 {
		log.Printf("Database migrated from version %d to %d", version, len(migrations))
	}
	return nil
}

// removeOld removes the files matching a pattern older than the given age
func removeOld(pattern string, age time.Duration) {
	paths, _ := filepath.Glob(pattern)
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > age {
			os.Remove(path)
		}
	}
}

// TempDir is a directory for temporary files, on the same file system as the data
func (s *Store) TempDir() string {
	return filepath.Join(s.dir, "tmp")
}

func (s *Store) Close() error {
	return s.db.Close()
}

type RunStatus string

const (
	RunRunning    RunStatus = "running"
	RunComplete   RunStatus = "complete"
	RunIncomplete RunStatus = "incomplete" // stopped at its deadline
	RunFailed     RunStatus = "failed"
)

type Run struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	StartedAt time.Time       `json:"startedAt"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
	Status    RunStatus       `json:"status"`
	Config    json.RawMessage `json:"config"`
	Bots      []Bot           `json:"bots"`
	// What played the run: the arena server image, and the ladder's revision
	ServerImage    string `json:"serverImage,omitempty"`
	LadderRevision string `json:"ladderRevision,omitempty"`
}

// SetRunVersions records what played a run
func (s *Store) SetRunVersions(id int64, serverImage string, ladderRevision string) error {
	_, err := s.db.Exec(`UPDATE runs SET server_image = ?, ladder_revision = ? WHERE id = ?`, serverImage, ladderRevision, id)
	return err
}

// CreateRun records the start of a run, with its frozen pool of bots
func (s *Store) CreateRun(kind string, config any, bots []Bot) (int64, error) {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO runs (kind, started_at, status, config) VALUES (?, ?, ?, ?)`,
		kind, time.Now(), RunRunning, string(configJSON))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()

	for _, bot := range bots {
		if _, err := tx.Exec(`INSERT INTO run_bots (run_id, team, image, commit_id) VALUES (?, ?, ?, ?)`,
			id, bot.Name, bot.Image, bot.Commit); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (s *Store) FinishRun(id int64, status RunStatus) error {
	_, err := s.db.Exec(`UPDATE runs SET ended_at = ?, status = ? WHERE id = ?`, time.Now(), status, id)
	return err
}

// MarkInterruptedRuns marks runs left running (e.g. by a restart of the ladder) as incomplete
func (s *Store) MarkInterruptedRuns() error {
	_, err := s.db.Exec(`UPDATE runs SET ended_at = ?, status = ? WHERE status = ?`, time.Now(), RunIncomplete, RunRunning)
	return err
}

func (s *Store) scanRuns(query string, args ...any) ([]Run, error) {
	rows, err := s.db.Query(`SELECT id, kind, started_at, ended_at, status, config, server_image, ladder_revision FROM runs `+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		var run Run
		var ended sql.NullTime
		var config string
		if err := rows.Scan(&run.ID, &run.Kind, &run.StartedAt, &ended, &run.Status, &config, &run.ServerImage, &run.LadderRevision); err != nil {
			return nil, err
		}
		if ended.Valid {
			run.EndedAt = &ended.Time
		}
		run.Config = json.RawMessage(config)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range runs {
		bots, err := s.db.Query(`SELECT team, image, commit_id FROM run_bots WHERE run_id = ? ORDER BY team`, runs[i].ID)
		if err != nil {
			return nil, err
		}
		for bots.Next() {
			var bot Bot
			if err := bots.Scan(&bot.Name, &bot.Image, &bot.Commit); err != nil {
				bots.Close()
				return nil, err
			}
			runs[i].Bots = append(runs[i].Bots, bot)
		}
		bots.Close()
	}
	return runs, nil
}

func (s *Store) Run(id int64) (*Run, error) {
	runs, err := s.scanRuns(`WHERE id = ?`, id)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

// Runs returns the most recent runs of a kind (or of all kinds if empty), most recent first
func (s *Store) Runs(kind string, limit int) ([]Run, error) {
	if kind == "" {
		return s.scanRuns(`ORDER BY id DESC LIMIT ?`, limit)
	}
	return s.scanRuns(`WHERE kind = ? ORDER BY id DESC LIMIT ?`, kind, limit)
}

// Kinds of matches: only ladder matches are rated
const (
	LadderMatch     = "ladder"
	TournamentMatch = "tournament"
	ManualMatch     = "manual" // played by hand, for testing
)

// MatchInfo is what the store needs to know about a match besides its result
type MatchInfo struct {
	Kind  string
	RunID int64 // 0 outside of runs (tournaments are runs)
	// What played the match: the arena server image, and the ladder's revision
	ServerImage    string
	LadderRevision string
}

// RecordMatch stores a played match: its results with the points of each
// bot, its report and the bot logs. The report file is moved into the store.
//
// A failed match (with an error, and no game result) is recorded as void:
// it does not count, but its bots and their output are kept.
func (s *Store) RecordMatch(info MatchInfo, result *MatchResult) (int64, error) {
	if info.Kind == "" {
		return 0, fmt.Errorf("the match has no kind")
	}
	void := result.Error != ""
	if !void && (result.Result == nil || result.ReportFile == "") {
		return 0, fmt.Errorf("the match has no result")
	}

	placements := make([]Placement, len(result.Bots))
	points := make([]int, len(result.Bots))
	turns := 0
	if !void {
		for i, bot := range result.Bots {
			if bot.Seat < 0 || bot.Seat >= len(result.Result.Players) {
				return 0, fmt.Errorf("bot %s has no seat in the game", bot.Name)
			}
			placements[i] = Placement{
				Flowers: result.Result.Players[bot.Seat].Flowers,
				Failed:  bot.Status != Finished,
			}
		}
		points = MatchPoints(placements)
		turns = int(result.Result.Turns)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO matches (run_id, kind, game_id, map, players, started_at, ended_at, turns, void, error, server_image, ladder_revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullID(info.RunID), info.Kind, result.GameID, result.Map, len(result.Bots), result.Started, result.Ended, turns, void, result.Error, info.ServerImage, info.LadderRevision)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()

	for i, bot := range result.Bots {
		if _, err := tx.Exec(`INSERT INTO match_bots (match_id, team, image, commit_id, seat, flowers, points, status, exit_code) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, bot.Name, bot.Image, bot.Commit, bot.Seat, placements[i].Flowers, points[i], bot.Status, bot.ExitCode); err != nil {
			return 0, err
		}
	}

	// Files first, durably: a match in the database always has its report.
	// Files of a match whose recording was interrupted may remain under the
	// same ID: they are replaced.
	os.RemoveAll(filepath.Dir(s.LogPath(id, "")))
	os.Remove(s.ReportPath(id))
	if !void {
		if err := moveFileDurably(result.ReportFile, s.ReportPath(id)); err != nil {
			return 0, err
		}
	}
	for _, bot := range result.Bots {
		if bot.Log == "" {
			continue
		}
		if err := writeGzip(s.LogPath(id, bot.Name), []byte(bot.Log)); err != nil {
			return 0, err
		}
	}

	return id, tx.Commit()
}

// moveFileDurably moves a file within the file system, durably
func moveFileDurably(from string, to string) error {
	file, err := os.Open(from)
	if err != nil {
		return err
	}
	err = file.Sync()
	file.Close()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	return syncDir(filepath.Dir(to))
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (s *Store) ReportPath(matchID int64) string {
	return filepath.Join(s.dir, "reports", fmt.Sprintf("%d.json.gz", matchID))
}

func (s *Store) LogPath(matchID int64, team string) string {
	return filepath.Join(s.dir, "logs", fmt.Sprint(matchID), safeFileName(team)+".log.gz")
}

func safeFileName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 || r == '.' {
			return '_'
		}
		return r
	}, name)
}

// writeGzip writes a compressed file durably: once it returns, the complete
// file is on disk, even if the machine crashes or loses power right after.
// The file is written under a temporary name, synced, then renamed, so a
// partial file never appears under the final name.
func writeGzip(path string, data []byte) error {
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	if _, err := zip.Write(data); err != nil {
		return err
	}
	if err := zip.Close(); err != nil {
		return err
	}
	return writeFileDurably(path, buffer.Bytes())
}

func writeFileDurably(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir makes the creation or renaming of files in a directory durable
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func readGzip(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	zip, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zip)
}

// Results returns the results of all valid matches of the given runs, ordered by match
func (s *Store) Results(runIDs []int64) ([]BotResult, error) {
	if len(runIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(runIDs)), ",")
	args := make([]any, len(runIDs))
	for i, id := range runIDs {
		args[i] = id
	}

	rows, err := s.db.Query(`
		SELECT m.id, m.run_id, m.map, m.players, b.team, b.commit_id, b.points, b.status
		FROM matches m JOIN match_bots b ON b.match_id = m.id
		WHERE m.void = 0 AND m.run_id IN (`+placeholders+`)
		ORDER BY m.id, b.seat`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BotResult
	for rows.Next() {
		var r BotResult
		if err := rows.Scan(&r.MatchID, &r.RunID, &r.Map, &r.Players, &r.Team, &r.Commit, &r.Points, &r.Status); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Leaderboard computes the leaderboard over the last `window` finished
// ladder runs, and returns it with these runs
func (s *Store) Leaderboard(window int) (Leaderboard, []Run, error) {
	// Runs without any counted match do not take a place in the window
	runs, err := s.scanRuns(`WHERE kind = 'ladder' AND status IN (?, ?)
		AND EXISTS (SELECT 1 FROM matches m WHERE m.run_id = runs.id AND m.void = 0)
		ORDER BY id DESC LIMIT ?`,
		RunComplete, RunIncomplete, window)
	if err != nil {
		return Leaderboard{}, nil, err
	}
	var ids []int64
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	results, err := s.Results(ids)
	if err != nil {
		return Leaderboard{}, nil, err
	}
	return NewLeaderboard(results), runs, nil
}

type MatchBot struct {
	Team     string    `json:"team"`
	Image    string    `json:"image"`
	Commit   string    `json:"commit"`
	Seat     int       `json:"seat"`
	Flowers  int       `json:"flowers"`
	Points   int       `json:"points"`
	Status   BotStatus `json:"status"`
	ExitCode int       `json:"exitCode"`
}

type MatchSummary struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	RunID     int64      `json:"runId,omitempty"`
	GameID    string     `json:"gameId"`
	Map       string     `json:"map"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   time.Time  `json:"endedAt"`
	Turns     int        `json:"turns"`
	Void      bool       `json:"void"`
	Error     string     `json:"error,omitempty"` // why the match failed or was voided
	Bots      []MatchBot `json:"bots"`            // by seat
	// The report and logs were deleted (see PruneFiles)
	FilesDeleted   bool   `json:"filesDeleted,omitempty"`
	ServerImage    string `json:"serverImage,omitempty"`
	LadderRevision string `json:"ladderRevision,omitempty"`
}

type MatchFilter struct {
	Kind   string
	RunID  int64
	Team   string
	Map    string
	Before int64 // only matches with a smaller ID, for paging
	Limit  int
}

// Matches returns matches, most recent first
func (s *Store) Matches(filter MatchFilter) ([]MatchSummary, error) {
	var where []string
	var args []any
	if filter.Kind != "" {
		where = append(where, "m.kind = ?")
		args = append(args, filter.Kind)
	}
	if filter.RunID != 0 {
		where = append(where, "m.run_id = ?")
		args = append(args, filter.RunID)
	}
	if filter.Team != "" {
		where = append(where, "EXISTS (SELECT 1 FROM match_bots t WHERE t.match_id = m.id AND t.team = ?)")
		args = append(args, filter.Team)
	}
	if filter.Map != "" {
		where = append(where, "m.map = ?")
		args = append(args, filter.Map)
	}
	if filter.Before != 0 {
		where = append(where, "m.id < ?")
		args = append(args, filter.Before)
	}
	query := `SELECT m.id, m.kind, COALESCE(m.run_id, 0), m.game_id, m.map, m.started_at, m.ended_at, m.turns, m.void, m.error,
		m.files_deleted, m.server_image, m.ladder_revision FROM matches m`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY m.id DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var matches []MatchSummary
	for rows.Next() {
		var m MatchSummary
		if err := rows.Scan(&m.ID, &m.Kind, &m.RunID, &m.GameID, &m.Map, &m.StartedAt, &m.EndedAt, &m.Turns, &m.Void, &m.Error,
			&m.FilesDeleted, &m.ServerImage, &m.LadderRevision); err != nil {
			rows.Close()
			return nil, err
		}
		matches = append(matches, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(matches) == 0 {
		return matches, nil
	}

	// The bots of all the matches at once
	index := make(map[int64]int)
	ids := make([]any, len(matches))
	for i, m := range matches {
		index[m.ID] = i
		ids[i] = m.ID
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	bots, err := s.db.Query(`SELECT match_id, team, image, commit_id, seat, flowers, points, status, exit_code FROM match_bots WHERE match_id IN (`+placeholders+`) ORDER BY match_id, seat, team`, ids...)
	if err != nil {
		return nil, err
	}
	defer bots.Close()
	for bots.Next() {
		var matchID int64
		var b MatchBot
		if err := bots.Scan(&matchID, &b.Team, &b.Image, &b.Commit, &b.Seat, &b.Flowers, &b.Points, &b.Status, &b.ExitCode); err != nil {
			return nil, err
		}
		m := &matches[index[matchID]]
		m.Bots = append(m.Bots, b)
	}
	return matches, bots.Err()
}

func (s *Store) Match(id int64) (*MatchSummary, error) {
	matches, err := s.Matches(MatchFilter{Before: id + 1, Limit: 1})
	if err != nil || len(matches) == 0 || matches[0].ID != id {
		return nil, err
	}
	return &matches[0], nil
}

// SetVoid marks a match as not counted (or counted again)
func (s *Store) SetVoid(id int64, void bool, reason string) error {
	_, err := s.db.Exec(`UPDATE matches SET void = ?, error = ? WHERE id = ?`, void, reason, id)
	return err
}

// ConsecutiveFailures returns how many of a bot's most recent matches with
// the given commit, in a row, it failed (crashed or did not join)
func (s *Store) ConsecutiveFailures(team string, commit string) (int, error) {
	rows, err := s.db.Query(`
		SELECT b.status FROM match_bots b JOIN matches m ON m.id = b.match_id
		WHERE b.team = ? AND b.commit_id = ? AND m.void = 0 AND m.kind = 'ladder'
		ORDER BY m.id DESC LIMIT 100`, team, commit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var status BotStatus
		if err := rows.Scan(&status); err != nil {
			return 0, err
		}
		if status == Finished {
			break
		}
		count++
	}
	return count, rows.Err()
}

// RatedMatches returns the valid ladder matches that ended before a time
// (all of them if zero), in order, as needed for ratings
func (s *Store) RatedMatches(before time.Time) ([]RatedMatch, error) {
	query := `SELECT m.id, m.ended_at, m.map, m.players, b.team, b.commit_id, b.flowers, b.status
		FROM matches m JOIN match_bots b ON b.match_id = m.id
		WHERE m.kind = 'ladder' AND m.void = 0`
	var args []any
	if !before.IsZero() {
		query += ` AND m.ended_at < ?`
		args = append(args, before)
	}
	rows, err := s.db.Query(query+` ORDER BY m.id, b.seat`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []RatedMatch
	for rows.Next() {
		var id int64
		var ended time.Time
		var format Format
		var bot RatedBot
		var status BotStatus
		if err := rows.Scan(&id, &ended, &format.Map, &format.Players, &bot.Team, &bot.Commit, &bot.Flowers, &status); err != nil {
			return nil, err
		}
		bot.Failed = status != Finished
		if len(matches) == 0 || matches[len(matches)-1].ID != id {
			matches = append(matches, RatedMatch{ID: id, Time: ended, Format: format.Key()})
		}
		last := &matches[len(matches)-1]
		last.Bots = append(last.Bots, bot)
	}
	return matches, rows.Err()
}

// ResultsSince returns the results of the valid ladder matches that ended
// since a time, ordered by match, for statistics
func (s *Store) ResultsSince(since time.Time) ([]BotResult, error) {
	rows, err := s.db.Query(`
		SELECT m.id, COALESCE(m.run_id, 0), m.map, m.players, b.team, b.commit_id, b.points, b.status
		FROM matches m JOIN match_bots b ON b.match_id = m.id
		WHERE m.kind = 'ladder' AND m.void = 0 AND m.ended_at >= ?
		ORDER BY m.id, b.seat`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BotResult
	for rows.Next() {
		var r BotResult
		if err := rows.Scan(&r.MatchID, &r.RunID, &r.Map, &r.Players, &r.Team, &r.Commit, &r.Points, &r.Status); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// MatchCounts returns how many valid ladder matches each team played in each
// format (see Format.Key) since a time
func (s *Store) MatchCounts(since time.Time) (map[string]map[string]int, error) {
	rows, err := s.db.Query(`SELECT b.team, m.map, m.players, COUNT(*)
		FROM matches m JOIN match_bots b ON b.match_id = m.id
		WHERE m.kind = 'ladder' AND m.void = 0 AND m.ended_at >= ?
		GROUP BY b.team, m.map, m.players`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]map[string]int)
	for rows.Next() {
		var team string
		var format Format
		var count int
		if err := rows.Scan(&team, &format.Map, &format.Players, &count); err != nil {
			return nil, err
		}
		if counts[team] == nil {
			counts[team] = make(map[string]int)
		}
		counts[team][format.Key()] = count
	}
	return counts, rows.Err()
}

// Played is what the ladder played over a period
type Played struct {
	Matches int      `json:"matches"`
	Void    int      `json:"void"`
	Busy    Duration `json:"busy"` // total duration of the matches
}

// PlayedSince returns what the ladder played since a time
func (s *Store) PlayedSince(since time.Time) (Played, error) {
	var a Played
	rows, err := s.db.Query(`SELECT void, started_at, ended_at FROM matches WHERE kind = 'ladder' AND ended_at >= ?`, since)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var void bool
		var started, ended time.Time
		if err := rows.Scan(&void, &started, &ended); err != nil {
			return a, err
		}
		if void {
			a.Void++
		} else {
			a.Matches++
		}
		a.Busy.Duration += ended.Sub(started)
	}
	return a, rows.Err()
}

// PruneFiles deletes the report and logs of the matches that are not among
// the last `keep` matches of any of their teams, and returns how many
// matches lost their files. Tournament matches keep theirs.
func (s *Store) PruneFiles(keep int) (int, error) {
	return s.deleteFiles(`SELECT m.id FROM matches m
		WHERE m.files_deleted = 0 AND m.kind != 'tournament' AND NOT EXISTS (
			SELECT 1 FROM (
				SELECT match_id, ROW_NUMBER() OVER (PARTITION BY team ORDER BY match_id DESC) AS n
				FROM match_bots
			) r WHERE r.match_id = m.id AND r.n <= ?)`, keep)
}

// DeleteOldestFiles deletes the report and logs of the `count` oldest
// matches that still have them (tournaments last), when disk space is low
func (s *Store) DeleteOldestFiles(count int) (int, error) {
	return s.deleteFiles(`SELECT id FROM matches WHERE files_deleted = 0
		ORDER BY kind = 'tournament', id LIMIT ?`, count)
}

func (s *Store) deleteFiles(query string, args ...any) (int, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// Marked first: if deleting is interrupted, files are left over, but the
	// site never links to missing ones
	for _, id := range ids {
		if _, err := s.db.Exec(`UPDATE matches SET files_deleted = 1 WHERE id = ?`, id); err != nil {
			return 0, err
		}
		os.Remove(s.ReportPath(id))
		os.RemoveAll(filepath.Dir(s.LogPath(id, "")))
	}
	return len(ids), nil
}

const buildsSchema = `
CREATE TABLE IF NOT EXISTS builds (
	id INTEGER PRIMARY KEY,
	team TEXT NOT NULL,
	tag TEXT NOT NULL,
	commit_id TEXT NOT NULL,
	image TEXT NOT NULL,
	status TEXT NOT NULL, -- ok, build-failed, smoke-failed
	started_at TIMESTAMP NOT NULL,
	ended_at TIMESTAMP NOT NULL,
	error TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS builds_team ON builds(team, commit_id);
`

func (s *Store) BuildLogPath(id int64) string {
	return filepath.Join(s.dir, "builds", fmt.Sprintf("%d.log.gz", id))
}

// RecordBuild stores a build and its log
func (s *Store) RecordBuild(build *Build, output string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO builds (team, tag, commit_id, image, status, started_at, ended_at, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		build.Team, build.Tag, build.Commit, build.Image, build.Status, build.StartedAt, build.EndedAt, build.Error)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()

	// The log is on disk before the build is committed
	if err := writeGzip(s.BuildLogPath(id), []byte(output)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) scanBuilds(query string, args ...any) ([]Build, error) {
	rows, err := s.db.Query(`SELECT id, team, tag, commit_id, image, status, started_at, ended_at, error FROM builds `+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var builds []Build
	for rows.Next() {
		var b Build
		if err := rows.Scan(&b.ID, &b.Team, &b.Tag, &b.Commit, &b.Image, &b.Status, &b.StartedAt, &b.EndedAt, &b.Error); err != nil {
			return nil, err
		}
		builds = append(builds, b)
	}
	return builds, rows.Err()
}

func firstBuild(builds []Build, err error) (*Build, error) {
	if err != nil || len(builds) == 0 {
		return nil, err
	}
	return &builds[0], nil
}

// BuildFor returns the latest build of a team's commit, or nil
func (s *Store) BuildFor(team string, commit string) (*Build, error) {
	return firstBuild(s.scanBuilds(`WHERE team = ? AND commit_id = ? ORDER BY id DESC LIMIT 1`, team, commit))
}

func (s *Store) LastBuild(team string) (*Build, error) {
	return firstBuild(s.scanBuilds(`WHERE team = ? ORDER BY id DESC LIMIT 1`, team))
}

func (s *Store) LastGoodBuild(team string) (*Build, error) {
	return firstBuild(s.scanBuilds(`WHERE team = ? AND status = ? ORDER BY id DESC LIMIT 1`, team, BuildOK))
}

// GoodBuilds returns a team's successful builds, most recent first
func (s *Store) GoodBuilds(team string) ([]Build, error) {
	return s.scanBuilds(`WHERE team = ? AND status = ? ORDER BY id DESC`, team, BuildOK)
}

// Builds returns a team's builds, most recent first
func (s *Store) Builds(team string, limit int) ([]Build, error) {
	return s.scanBuilds(`WHERE team = ? ORDER BY id DESC LIMIT ?`, team, limit)
}

// DeleteBuild forgets a build, so that its commit is built again at the next run
func (s *Store) DeleteBuild(id int64) error {
	_, err := s.db.Exec(`DELETE FROM builds WHERE id = ?`, id)
	return err
}

// SavePoolState keeps the state of the teams at the last pool resolution, for the website
func (s *Store) SavePoolState(state *PoolState) error {
	data, err := json.MarshalIndent(state, "", "\t")
	if err != nil {
		return err
	}
	return writeFileDurably(filepath.Join(s.dir, "teams.json"), data)
}

// PoolState returns the state of the teams at the last pool resolution
// (empty if there was none)
func (s *Store) PoolState() (*PoolState, error) {
	state := &PoolState{}
	data, err := os.ReadFile(filepath.Join(s.dir, "teams.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	return state, json.Unmarshal(data, state)
}

// TeamStates returns the state of each team at the last pool resolution
func (s *Store) TeamStates() ([]TeamState, error) {
	state, err := s.PoolState()
	return state.Teams, err
}

// Counts returns the number of runs, matches and builds
func (s *Store) Counts() ([3]int, error) {
	var counts [3]int
	err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM runs), (SELECT COUNT(*) FROM matches), (SELECT COUNT(*) FROM builds)`).
		Scan(&counts[0], &counts[1], &counts[2])
	return counts, err
}
