package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestWebPages(t *testing.T) {
	store := newTestStore(t)
	config := &Config{Timezone: "Europe/Helsinki", Ladder: testSettings(Format{"m", 3})}

	a := Bot{Name: "alpha", Image: "img", Commit: "0123456789abcdef"}
	b := Bot{Name: "beta", Image: "img", Commit: "b"}
	c := Bot{Name: "gamma", Image: "img", Commit: "c"}
	match, _ := store.RecordMatch(MatchInfo{Kind: LadderMatch}, fakeResult("m", []Bot{a, b, c}, []uint{5, 3, 0}, []BotStatus{Finished, Finished, Crashed}))
	store.RecordBuild(&Build{Team: "alpha", Tag: "v1", Commit: "0123456789abcdef", Status: BuildOK}, "build output")
	store.SavePoolState(&PoolState{Teams: []TeamState{{Team: Team{Name: "alpha", Repo: "https://example.com/alpha", Members: []string{"x"}}, Bot: &a}}})

	web, err := NewWeb(config, store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(web.Handler())
	defer server.Close()

	get := func(path string, status int, contains ...string) {
		t.Helper()
		res, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != status {
			t.Errorf("%s: expected status %d, got %d", path, status, res.StatusCode)
		}
		for _, s := range contains {
			if !strings.Contains(string(body), s) {
				t.Errorf("%s: expected %q in:\n%s", path, s, body)
			}
		}
	}

	id := func(i int64) string { return strconv.FormatInt(i, 10) }

	get("/", 200, "alpha", "60.0", "01234567", "Head to head", "100%", "m, 3 players", "1 matches")
	get("/runs", 200, "No tournaments yet")
	get("/runs/999", 404)
	get("/matches", 200, "alpha 60", "gamma 0")
	get("/matches?team=gamma", 200, "gamma")
	get("/matches/"+id(match), 200, "crashed", "Download the report", "/logs/alpha")
	get("/matches/999", 404)
	get("/matches/"+id(match)+"/report.json", 200, `{"id":"game"}`)
	get("/matches/"+id(match)+"/logs/alpha", 200, "log of alpha")
	get("/matches/"+id(match)+"/logs/..%2F..%2Fx", 404)
	get("/teams/alpha", 200, "https://example.com/alpha", "v1", "Recent matches", "Rating", "1 / 20")
	get("/teams/nobody", 404)
	get("/builds/1/log", 200, "build output")
	get("/api/leaderboard", 200, `"team":"alpha"`, `"perFormat"`)
	get("/api/matches?team=alpha", 200, `"gameId":"game"`)
	get("/api/teams", 200, `"name":"alpha"`)
	get("/static/style.css", 200, "body")
	get("/screen", 200, "/api/leaderboard")

	// Expired replays
	store.PruneFiles(1)
	newer, _ := store.RecordMatch(MatchInfo{Kind: LadderMatch}, fakeResult("m", []Bot{a, b, c}, []uint{5, 3, 0}, []BotStatus{Finished, Finished, Finished}))
	store.PruneFiles(1)
	get("/matches/"+id(match), 200, "expired")
	get("/matches/"+id(newer), 200, "Watch the replay")

	// Tournaments are listed, but not counted in the ladder
	tournament, _ := store.CreateRun("tournament", TournamentConfig{Name: "Lunch cup"}, []Bot{a, c})
	store.RecordMatch(MatchInfo{Kind: TournamentMatch, RunID: tournament}, fakeResult("m", []Bot{a, c}, []uint{0, 9}, []BotStatus{Finished, Finished}))
	store.FinishRun(tournament, RunComplete)
	get("/runs", 200, "Lunch cup")
	get("/runs/"+id(tournament), 200, "Lunch cup", "not counted")
	get("/", 200, "100%", "2 matches")
}
