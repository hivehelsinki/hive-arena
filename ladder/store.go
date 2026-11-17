package main

import (
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
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
	for _, sub := range []string{"reports", "logs", "builds"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "ladder.db")+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema + buildsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating the schema: %w", err)
	}
	return &Store{db: db, dir: dir}, nil
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
	rows, err := s.db.Query(`SELECT id, kind, started_at, ended_at, status, config FROM runs `+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		var run Run
		var ended sql.NullTime
		var config string
		if err := rows.Scan(&run.ID, &run.Kind, &run.StartedAt, &ended, &run.Status, &config); err != nil {
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

// RecordMatch stores a played match: its results with the points of each
// bot, its report and the bot logs. runID is 0 for matches outside of runs.
func (s *Store) RecordMatch(runID int64, result *MatchResult) (int64, error) {
	placements := make([]Placement, len(result.Bots))
	for i, bot := range result.Bots {
		placements[i] = Placement{
			Flowers: result.Result.Players[bot.Seat].Flowers,
			Failed:  bot.Status != Finished,
		}
	}
	points := MatchPoints(placements)

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO matches (run_id, game_id, map, players, started_at, ended_at, turns) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		nullID(runID), result.GameID, result.Map, len(result.Bots), result.Started, result.Ended, result.Result.Turns)
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

	// Files first: a match in the database always has its report
	if err := writeGzip(s.ReportPath(id), result.Report); err != nil {
		return 0, err
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

func writeGzip(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	zip := gzip.NewWriter(file)
	if _, err := zip.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := zip.Close(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
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
	runs, err := s.scanRuns(`WHERE kind = 'ladder' AND status IN (?, ?) ORDER BY id DESC LIMIT ?`,
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
	RunID     int64      `json:"runId,omitempty"`
	GameID    string     `json:"gameId"`
	Map       string     `json:"map"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   time.Time  `json:"endedAt"`
	Turns     int        `json:"turns"`
	Void      bool       `json:"void"`
	Bots      []MatchBot `json:"bots"` // by seat
}

type MatchFilter struct {
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
	query := `SELECT m.id, COALESCE(m.run_id, 0), m.game_id, m.map, m.started_at, m.ended_at, m.turns, m.void FROM matches m`
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
		if err := rows.Scan(&m.ID, &m.RunID, &m.GameID, &m.Map, &m.StartedAt, &m.EndedAt, &m.Turns, &m.Void); err != nil {
			rows.Close()
			return nil, err
		}
		matches = append(matches, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range matches {
		bots, err := s.db.Query(`SELECT team, image, commit_id, seat, flowers, points, status, exit_code FROM match_bots WHERE match_id = ? ORDER BY seat`, matches[i].ID)
		if err != nil {
			return nil, err
		}
		for bots.Next() {
			var b MatchBot
			if err := bots.Scan(&b.Team, &b.Image, &b.Commit, &b.Seat, &b.Flowers, &b.Points, &b.Status, &b.ExitCode); err != nil {
				bots.Close()
				return nil, err
			}
			matches[i].Bots = append(matches[i].Bots, b)
		}
		bots.Close()
	}
	return matches, nil
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
		WHERE b.team = ? AND b.commit_id = ? AND m.void = 0
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
	res, err := s.db.Exec(`INSERT INTO builds (team, tag, commit_id, image, status, started_at, ended_at, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		build.Team, build.Tag, build.Commit, build.Image, build.Status, build.StartedAt, build.EndedAt, build.Error)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, writeGzip(s.BuildLogPath(id), []byte(output))
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

// SaveTeamStates keeps the state of each team's bot at the last pool resolution, for the website
func (s *Store) SaveTeamStates(states []TeamState) error {
	data, err := json.MarshalIndent(states, "", "\t")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "teams.json")
	if err := os.WriteFile(path+".tmp", data, 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (s *Store) TeamStates() ([]TeamState, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "teams.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var states []TeamState
	return states, json.Unmarshal(data, &states)
}
