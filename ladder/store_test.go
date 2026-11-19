package main

import (
	"compress/gzip"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestStoreMatches(t *testing.T) {
	store := newTestStore(t)

	a := Bot{Name: "a", Image: "img-a", Commit: "c1"}
	b := Bot{Name: "b", Image: "img-b", Commit: "c2"}
	c := Bot{Name: "c", Image: "img-c", Commit: "c3"}

	m1, err := store.RecordMatch(MatchInfo{}, fakeResult("m", []Bot{a, b, c}, []uint{5, 3, 0}, []BotStatus{Finished, Finished, Finished}))
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := store.RecordMatch(MatchInfo{}, fakeResult("m", []Bot{a, b, c}, []uint{5, 3, 9}, []BotStatus{Finished, Finished, Crashed}))

	// c crashed, so it is last despite its flowers
	match, err := store.Match(m2)
	if err != nil || match == nil {
		t.Fatalf("match not found: %v", err)
	}
	if match.Bots[0].Points != 60 || match.Bots[1].Points != 30 || match.Bots[2].Points != 0 || match.Bots[2].Status != Crashed {
		t.Errorf("unexpected points: %+v", match.Bots)
	}

	// Match details and files
	match, _ = store.Match(m1)
	if len(match.Bots) != 3 || match.Bots[0].Points != 60 || match.Bots[0].Commit != "c1" {
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

	// Void matches are not rated
	store.SetVoid(m1, true, "test")
	if rated, _ := store.RatedMatches(time.Time{}); len(rated) != 1 || rated[0].ID != m2 {
		t.Errorf("a void match should not be rated: %+v", rated)
	}

	// Filters
	if matches, _ := store.Matches(MatchFilter{Map: "other"}); len(matches) != 0 {
		t.Errorf("expected no match on another map, got %d", len(matches))
	}
	if matches, _ := store.Matches(MatchFilter{Team: "a", Limit: 1}); len(matches) != 1 || matches[0].ID != m2 {
		t.Errorf("expected the most recent match, got %+v", matches)
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

	failed := &MatchResult{
		GameID: "game", Map: "m", Started: time.Now(), Ended: time.Now(),
		Error: "the match took more than 30m0s",
		Bots: []BotOutcome{
			{Bot: a, Seat: -1, Status: Finished, ExitCode: -1, Log: "log of a"},
			{Bot: b, Seat: -1, Status: Crashed, ExitCode: 1},
		},
	}
	id, err := store.RecordMatch(MatchInfo{}, failed)
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
	if rated, _ := store.RatedMatches(time.Time{}); len(rated) != 0 {
		t.Errorf("a void match should not be rated: %+v", rated)
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
	if _, err := store.RecordMatch(MatchInfo{}, result); err == nil {
		t.Error("a bot without a seat should be an error, not a crash")
	}
}

func TestMigrations(t *testing.T) {
	dir := t.TempDir()

	// A database of the first version, with data
	db, err := sql.Open("sqlite", filepath.Join(dir, "ladder.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, statement := range []string{
		migrations[0],
		`INSERT INTO runs (id, kind, started_at, status, config) VALUES (1, 'ladder', ?, 'complete', '{}'), (2, 'tournament', ?, 'complete', '{}')`,
		`INSERT INTO matches (id, run_id, game_id, map, players, started_at, ended_at, turns) VALUES
			(1, 1, 'g', 'm', 2, ?, ?, 10), (2, 2, 'g', 'm', 2, ?, ?, 10), (3, NULL, 'g', 'm', 2, ?, ?, 10)`,
		`PRAGMA user_version = 1`,
	} {
		args := []any{}
		for range strings.Count(statement, "?") {
			args = append(args, now)
		}
		if _, err := db.Exec(statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	store.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != len(migrations) {
		t.Errorf("expected version %d, got %d", len(migrations), version)
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "ladder-v1-*.db")); len(backups) != 1 {
		t.Errorf("expected a copy of the database before the migration, got %v", backups)
	}
	// Matches of ladder runs are ladder matches; those of tournaments, or
	// played by hand, are hidden
	if match, _ := store.Match(1); match == nil {
		t.Error("the match of the ladder run should be kept")
	}
	for _, id := range []int64{2, 3} {
		if match, _ := store.Match(id); match != nil {
			t.Errorf("match %d should be hidden: %+v", id, match)
		}
	}

	// A database from a newer ladder is refused
	store.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations)+1))
	if other, err := OpenStore(dir); err == nil {
		other.Close()
		t.Error("a database from a newer version should be refused")
	}
}

func TestRatedMatchesAndCounts(t *testing.T) {
	store := newTestStore(t)
	a := Bot{Name: "a", Image: "img-a", Commit: "c1"}
	b := Bot{Name: "b", Image: "img-b", Commit: "c2"}
	ok := []BotStatus{Finished, Finished}
	ladder := MatchInfo{ServerImage: "sha256:s", LadderRevision: "r"}

	first, _ := store.RecordMatch(ladder, fakeResult("m", []Bot{a, b}, []uint{1, 2}, ok))
	store.RecordMatch(ladder, fakeResult("m", []Bot{a, b}, []uint{3, 2}, []BotStatus{Finished, Crashed}))
	store.RecordMatch(ladder, fakeResult("other", []Bot{b, a}, []uint{0, 0}, ok))
	// Not rated: void matches
	void, _ := store.RecordMatch(ladder, fakeResult("m", []Bot{a, b}, []uint{1, 2}, ok))
	store.SetVoid(void, true, "test")

	matches, err := store.RatedMatches(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 3 || matches[0].ID != first || matches[0].Format != "m:2" || matches[2].Format != "other:2" {
		t.Fatalf("unexpected rated matches: %+v", matches)
	}
	if bots := matches[1].Bots; len(bots) != 2 || bots[0].Team != "a" || bots[0].Flowers != 3 || bots[0].Failed || !bots[1].Failed || bots[1].Commit != "c2" {
		t.Errorf("unexpected bots: %+v", bots)
	}
	if before, _ := store.RatedMatches(time.Now().Add(-time.Hour)); len(before) != 0 {
		t.Errorf("expected no matches before an hour ago, got %d", len(before))
	}

	counts, err := store.MatchCounts(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if counts["a"]["m:2"] != 2 || counts["b"]["other:2"] != 1 {
		t.Errorf("unexpected counts: %v", counts)
	}
	if counts, _ := store.MatchCounts(time.Now().Add(time.Hour)); len(counts) != 0 {
		t.Errorf("expected no counts in the future: %v", counts)
	}

	played, err := store.PlayedSince(time.Now().Add(-24 * time.Hour))
	if err != nil || played.Matches != 3 || played.Void != 1 {
		t.Errorf("unexpected activity: %+v, %v", played, err)
	}

	match, _ := store.Match(first)
	if match.ServerImage != "sha256:s" || match.LadderRevision != "r" {
		t.Errorf("unexpected match: %+v", match)
	}
}

func TestPruneFiles(t *testing.T) {
	store := newTestStore(t)
	a, b, c := Bot{Name: "a"}, Bot{Name: "b"}, Bot{Name: "c"}
	ok := []BotStatus{Finished, Finished}
	ladder := MatchInfo{}

	ab1, _ := store.RecordMatch(ladder, fakeResult("m", []Bot{a, b}, []uint{1, 2}, ok))
	bc, _ := store.RecordMatch(ladder, fakeResult("m", []Bot{b, c}, []uint{1, 2}, ok))
	ab2, _ := store.RecordMatch(ladder, fakeResult("m", []Bot{a, b}, []uint{1, 2}, ok))
	ab3, _ := store.RecordMatch(ladder, fakeResult("m", []Bot{a, b}, []uint{1, 2}, ok))

	// Keeping 2 matches per team: a and b keep ab2 and ab3, c keeps bc
	n, err := store.PruneFiles(2)
	if err != nil || n != 1 {
		t.Fatalf("expected one match to lose its files, got %d, %v", n, err)
	}
	for _, id := range []int64{bc, ab2, ab3} {
		if _, err := os.Stat(store.ReportPath(id)); err != nil {
			t.Errorf("match %d should keep its report", id)
		}
	}
	if _, err := os.Stat(store.ReportPath(ab1)); !os.IsNotExist(err) {
		t.Error("the report of the first match should be deleted")
	}
	if _, err := os.Stat(filepath.Dir(store.LogPath(ab1, "a"))); !os.IsNotExist(err) {
		t.Error("the logs of the first match should be deleted")
	}
	if match, _ := store.Match(ab1); !match.FilesDeleted {
		t.Error("the match should be marked as without files")
	}

	// Low disk space: the oldest first
	if n, _ := store.DeleteOldestFiles(1); n != 1 {
		t.Errorf("expected one match, got %d", n)
	}
	if match, _ := store.Match(bc); !match.FilesDeleted {
		t.Error("the oldest ladder match should lose its files first")
	}
}
