package main

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hivehelsinki/hive-arena/arena"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// fakeResult builds a match result where bots are given with their flowers, seated in order
func fakeResult(mapname string, bots []Bot, flowers []uint, statuses []BotStatus) *MatchResult {
	result := &MatchResult{
		GameID:  "game",
		Map:     mapname,
		Started: time.Now(),
		Ended:   time.Now(),
		Result:  &arena.GameResult{Turns: 100},
	}
	file, _ := os.CreateTemp("", "report-*.json.gz")
	zip := gzip.NewWriter(file)
	zip.Write([]byte(`{"id":"game"}`))
	zip.Close()
	file.Close()
	result.ReportFile = file.Name()
	for i, bot := range bots {
		result.Result.Players = append(result.Result.Players, arena.PlayerResult{Name: bot.Name, Flowers: flowers[i]})
		result.Bots = append(result.Bots, BotOutcome{Bot: bot, Seat: i, Status: statuses[i], Log: "log of " + bot.Name})
	}
	return result
}

func TestStoreRunsAndLeaderboard(t *testing.T) {
	store := newTestStore(t)

	a := Bot{Name: "a", Image: "img-a", Commit: "c1"}
	b := Bot{Name: "b", Image: "img-b", Commit: "c2"}
	c := Bot{Name: "c", Image: "img-c", Commit: "c3"}

	ok := []BotStatus{Finished, Finished, Finished}

	// An old run, outside of the window
	old, _ := store.CreateRun("ladder", map[string]any{"repeats": 1}, []Bot{a, b, c})
	store.RecordMatch(old, fakeResult("m", []Bot{a, b, c}, []uint{0, 0, 9}, ok))
	store.FinishRun(old, RunComplete)

	run1, err := store.CreateRun("ladder", map[string]any{"repeats": 1}, []Bot{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	m1, err := store.RecordMatch(run1, fakeResult("m", []Bot{a, b, c}, []uint{5, 3, 0}, ok))
	if err != nil {
		t.Fatal(err)
	}
	store.FinishRun(run1, RunComplete)

	run2, _ := store.CreateRun("ladder", map[string]any{"repeats": 1}, []Bot{a, b, c})
	store.RecordMatch(run2, fakeResult("m", []Bot{a, b, c}, []uint{5, 3, 9}, []BotStatus{Finished, Finished, Crashed}))
	store.FinishRun(run2, RunIncomplete)

	// Still running: not counted
	run3, _ := store.CreateRun("ladder", nil, []Bot{a, b, c})
	store.RecordMatch(run3, fakeResult("m", []Bot{a, b, c}, []uint{0, 0, 9}, ok))

	board, runs, err := store.Leaderboard(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != run2 || runs[1].ID != run1 || len(runs[0].Bots) != 3 {
		t.Fatalf("unexpected runs in the window: %+v", runs)
	}

	// a: 60, 60 / b: 30, 30 / c: 0 (last), 0 (crashed)
	means := map[string]float64{}
	for _, s := range board.Standings {
		means[s.Team] = s.Score.Mean
	}
	if means["a"] != 60 || means["b"] != 30 || means["c"] != 0 || board.Standings[0].Team != "a" {
		t.Errorf("unexpected standings: %+v", board.Standings)
	}

	// Match details and files
	match, err := store.Match(m1)
	if err != nil || match == nil {
		t.Fatalf("match not found: %v", err)
	}
	if match.RunID != run1 || len(match.Bots) != 3 || match.Bots[0].Points != 60 || match.Bots[0].Commit != "c1" {
		t.Errorf("unexpected match: %+v", match)
	}
	report, err := readGzip(store.ReportPath(m1))
	if err != nil || string(report) != `{"id":"game"}` {
		t.Errorf("unexpected report: %s, %v", report, err)
	}
	log, err := readGzip(store.LogPath(m1, "b"))
	if err != nil || string(log) != "log of b" {
		t.Errorf("unexpected log: %s, %v", log, err)
	}

	// Void matches are not counted, and a run without counted matches
	// leaves its place in the window to the previous one
	store.SetVoid(m1, true, "test")
	board, runs, _ = store.Leaderboard(2)
	if len(runs) != 2 || runs[0].ID != run2 || runs[1].ID != old {
		t.Errorf("unexpected runs in the window: %+v", runs)
	}
	for _, s := range board.Standings {
		if s.Score.Games != 2 {
			t.Errorf("expected the matches of runs %d and %d: %+v", run2, old, s)
		}
	}

	// Filters
	if matches, _ := store.Matches(MatchFilter{RunID: run2}); len(matches) != 1 {
		t.Errorf("expected 1 match in run 2, got %d", len(matches))
	}
	if matches, _ := store.Matches(MatchFilter{Team: "a", Limit: 2}); len(matches) != 2 || matches[0].ID < matches[1].ID {
		t.Errorf("expected the 2 most recent matches, got %+v", matches)
	}

	// Interrupted runs
	store.MarkInterruptedRuns()
	if run, _ := store.Run(run3); run.Status != RunIncomplete || run.EndedAt == nil {
		t.Errorf("expected the running run to be marked incomplete: %+v", run)
	}
}

func TestStoreMatchWithoutRun(t *testing.T) {
	store := newTestStore(t)
	id, err := store.RecordMatch(0, fakeResult("m", []Bot{{Name: "x"}, {Name: "y"}}, []uint{1, 2}, []BotStatus{Finished, Finished}))
	if err != nil {
		t.Fatal(err)
	}
	match, _ := store.Match(id)
	if match == nil || match.RunID != 0 || match.Bots[1].Points != 60 {
		t.Errorf("unexpected match: %+v", match)
	}
}

func TestWriteGzipIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sub/file.gz"
	if err := writeGzip(path, []byte("content")); err != nil {
		t.Fatal(err)
	}
	if data, err := readGzip(path); err != nil || string(data) != "content" {
		t.Errorf("unexpected content: %q, %v", data, err)
	}
	// Overwriting keeps a complete file, and no temporary file is left
	if err := writeGzip(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if data, _ := readGzip(path); string(data) != "new" {
		t.Errorf("unexpected content: %q", data)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temporary file left behind")
	}
}

func TestRecordVoidMatch(t *testing.T) {
	store := newTestStore(t)
	a := Bot{Name: "a", Image: "img-a", Commit: "c1"}
	b := Bot{Name: "b", Image: "img-b", Commit: "c2"}

	run, _ := store.CreateRun("ladder", nil, []Bot{a, b})
	failed := &MatchResult{
		GameID: "game", Map: "m", Started: time.Now(), Ended: time.Now(),
		Error: "the match took more than 30m0s",
		Bots: []BotOutcome{
			{Bot: a, Seat: -1, Status: Finished, ExitCode: -1, Log: "log of a"},
			{Bot: b, Seat: -1, Status: Crashed, ExitCode: 1},
		},
	}
	id, err := store.RecordMatch(run, failed)
	if err != nil {
		t.Fatal(err)
	}

	match, err := store.Match(id)
	if err != nil || match == nil {
		t.Fatal(err)
	}
	if !match.Void || match.Error != failed.Error || len(match.Bots) != 2 {
		t.Errorf("expected a void match with its error and bots: %+v", match)
	}
	if log, err := readGzip(store.LogPath(id, "a")); err != nil || string(log) != "log of a" {
		t.Errorf("expected the log of a: %q, %v", log, err)
	}

	// Not counted, and not a failure of the bots
	results, _ := store.Results([]int64{run})
	if len(results) != 0 {
		t.Errorf("a void match should not count: %+v", results)
	}
	if n, _ := store.ConsecutiveFailures("b", "c2"); n != 0 {
		t.Errorf("a void match should not count as a failure, got %d", n)
	}
}

func TestRecordMatchRejectsUnseatedBots(t *testing.T) {
	store := newTestStore(t)
	a := Bot{Name: "a", Image: "img-a", Commit: "c1"}
	b := Bot{Name: "b", Image: "img-b", Commit: "c2"}

	result := fakeResult("m", []Bot{a, b}, []uint{1, 2}, []BotStatus{Finished, Finished})
	defer result.RemoveReport()
	result.Bots[1].Seat = -1
	if _, err := store.RecordMatch(0, result); err == nil {
		t.Error("a bot without a seat should be an error, not a crash")
	}
}

func TestMigrations(t *testing.T) {
	dir := t.TempDir()

	// A database of the first version, with data
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP INDEX matches_map; CREATE TABLE runs_old AS SELECT id, kind, started_at, ended_at, status, config FROM runs; DROP TABLE run_bots; DROP TABLE runs;
		ALTER TABLE runs_old RENAME TO runs; PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	store.db.Exec(`INSERT INTO runs (kind, started_at, status, config) VALUES ('ladder', ?, 'complete', '{}')`, time.Now())
	store.Close()

	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	store.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != len(migrations) {
		t.Errorf("expected version %d, got %d", len(migrations), version)
	}
	if err := store.SetRunVersions(1, "sha256:x", "abc"); err != nil {
		t.Error(err)
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "ladder-v1-*.db")); len(backups) != 1 {
		t.Errorf("expected a copy of the database before the migration, got %v", backups)
	}

	// A database from a newer ladder is refused
	store.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations)+1))
	if other, err := OpenStore(dir); err == nil {
		other.Close()
		t.Error("a database from a newer version should be refused")
	}
}
