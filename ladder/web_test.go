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
	config := &Config{Schedule: Schedule{
		Times: []string{"08:00"}, Timezone: "Europe/Helsinki",
		Formats: []Format{{"m", 3}}, Repeats: 1, Window: 2, MaxFailures: 3,
	}}

	a := Bot{Name: "alpha", Image: "img", Commit: "0123456789abcdef"}
	b := Bot{Name: "beta", Image: "img", Commit: "b"}
	c := Bot{Name: "gamma", Image: "img", Commit: "c"}
	run, _ := store.CreateRun("ladder", config.Schedule, []Bot{a, b, c})
	match, _ := store.RecordMatch(MatchInfo{Kind: LadderMatch, RunID: run}, fakeResult("m", []Bot{a, b, c}, []uint{5, 3, 0}, []BotStatus{Finished, Finished, Crashed}))
	store.FinishRun(run, RunComplete)
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

	get("/", 200, "alpha", "60.0", "01234567", "Head to head", "100%")
	get("/runs", 200, "complete")
	get("/runs/"+id(run), 200, "alpha", "gamma")
	get("/runs/999", 404)
	get("/matches", 200, "alpha 60", "gamma 0")
	get("/matches?team=gamma", 200, "gamma")
	get("/matches/"+id(match), 200, "crashed", "Download the report", "/logs/alpha")
	get("/matches/999", 404)
	get("/matches/"+id(match)+"/report.json", 200, `{"id":"game"}`)
	get("/matches/"+id(match)+"/logs/alpha", 200, "log of alpha")
	get("/matches/"+id(match)+"/logs/..%2F..%2Fx", 404)
	get("/teams/alpha", 200, "https://example.com/alpha", "v1", "Recent matches")
	get("/teams/nobody", 404)
	get("/builds/1/log", 200, "build output")
	get("/api/leaderboard", 200, `"team":"alpha"`)
	get("/api/matches?team=alpha", 200, `"gameId":"game"`)
	get("/api/runs", 200, `"status":"complete"`)
	get("/api/teams", 200, `"name":"alpha"`)
	get("/static/style.css", 200, "body")
	get("/screen", 200, "/api/leaderboard")

	// Tournaments are listed with the runs, but not counted in the leaderboard
	tournament, _ := store.CreateRun("tournament", TournamentConfig{Name: "Lunch cup"}, []Bot{a, c})
	store.RecordMatch(MatchInfo{Kind: TournamentMatch, RunID: tournament}, fakeResult("m", []Bot{a, c}, []uint{0, 9}, []BotStatus{Finished, Finished}))
	store.FinishRun(tournament, RunComplete)
	get("/runs", 200, "Lunch cup", "tournament")
	get("/runs/"+id(tournament), 200, "Lunch cup", "not counted")
	get("/", 200, "100%")
}
