package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// A player that records what it plays. Ladder matches (not real time) last
// until they are interrupted when block is set; manual matches end at once,
// unless holdManual is set.
type recordingPlayer struct {
	mutex      sync.Mutex
	matches    []Match
	block      bool
	holdManual bool
	ladder     chan struct{} // receives a value when a ladder match starts
}

func (p *recordingPlayer) Run(ctx context.Context, match Match) (*MatchResult, error) {
	p.mutex.Lock()
	p.matches = append(p.matches, match)
	p.mutex.Unlock()
	if !match.RealTime && p.block || match.RealTime && p.holdManual {
		if !match.RealTime && p.ladder != nil {
			p.ladder <- struct{}{}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if match.Live != nil {
		match.Live.Start("game", match.Map, time.Now(), []string{match.Bots[0].Name, match.Bots[1].Name})
		match.Live.Add([]byte(`{"turn":0}`))
		match.Live.Add([]byte(`{"turn":1}`))
		match.Live.End()
	}
	return fakeResult(match.Map, match.Bots, []uint{2, 1}, []BotStatus{Finished, Finished}), nil
}

func (p *recordingPlayer) played() []Match {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return append([]Match(nil), p.matches...)
}

func testManualMatch(t *testing.T) *ManualMatch {
	t.Helper()
	bots := []ManualBot{
		{TeamVersion: TeamVersion{Team: "bot0", Tag: "v1", Commit: "c", Image: "img"}, Name: "Bot zéro 🐝", Rank: 1},
		{TeamVersion: TeamVersion{Team: "bot1", Tag: "v2", Commit: "c", Image: "img"}, Name: "Bot one", Rank: 2},
	}
	match, err := NewManualMatch("m", bots, nil)
	if err != nil {
		t.Fatal(err)
	}
	return match
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func manualStatus(g *ManualGame) string {
	m, _ := g.Current()
	if m == nil {
		return ""
	}
	return m.Status
}

func TestManualMatchIsPlayedEvenWhenPaused(t *testing.T) {
	player := &recordingPlayer{}
	ladder := newTestLadder(t, testBots(2), player)
	settings := ladder.Settings.Get()
	settings.Paused = true
	ladder.Settings = NewLiveSettings("", settings)
	ladder.Manual = NewManualGame()
	match := testManualMatch(t)
	if err := ladder.Manual.Start(match); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ladder.Serve(ctx, nil)
		close(done)
	}()
	waitFor(t, "the manual match", func() bool { return manualStatus(ladder.Manual) == ManualFinished })
	cancel()
	<-done

	played := player.played()
	if len(played) != 1 || !played[0].RealTime || played[0].Live == nil {
		t.Fatalf("expected one real-time match followed live, got %+v", played)
	}
	m, live := ladder.Manual.Current()
	recorded, _ := ladder.Store.Match(m.MatchID)
	if recorded == nil || recorded.Kind != KindManual {
		t.Fatalf("expected the match to be recorded as manual: %+v", recorded)
	}

	// Recorded, but not counted for the ladder
	if rated, _ := ladder.Store.RatedMatches(time.Time{}); len(rated) != 0 {
		t.Errorf("manual matches should not be rated: %+v", rated)
	}
	if results, _ := ladder.Store.ResultsSince(time.Time{}); len(results) != 0 {
		t.Errorf("manual matches should not count for head-to-head: %+v", results)
	}
	if counts, _ := ladder.Store.MatchCounts(time.Time{}, nil); len(counts) != 0 {
		t.Errorf("manual matches should not count for quotas: %+v", counts)
	}

	// Watched live, with the teams' names
	states := live.Since(0)
	names := map[string]string{"bot0": "Bot zéro 🐝", "bot1": "Bot one"}
	if len(states.States) != 2 || !states.Over || states.Players[0] != names[played[0].Bots[0].Name] {
		t.Errorf("unexpected live states: %+v", states)
	}

	// Closed once over: another one can start
	if !ladder.Manual.Close() {
		t.Error("a finished match should close")
	}
	if m, _ := ladder.Manual.Current(); m != nil {
		t.Errorf("expected no manual match after closing: %+v", m)
	}
}

func TestManualMatchInterruptsTheLadderMatch(t *testing.T) {
	player := &recordingPlayer{block: true, ladder: make(chan struct{}, 10)}
	ladder := newTestLadder(t, testBots(2), player)
	ladder.Manual = NewManualGame()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ladder.Serve(ctx, nil)

	<-player.ladder
	ladder.Manual.Start(testManualMatch(t))
	waitFor(t, "the manual match", func() bool { return manualStatus(ladder.Manual) == ManualFinished })
	// The ladder goes on (and blocks on its next match)
	<-player.ladder

	matches, _ := ladder.Store.Matches(MatchFilter{})
	if len(matches) != 1 || matches[0].Kind != KindManual {
		t.Errorf("expected only the manual match to be recorded (the interrupted ladder match is lost): %+v", matches)
	}
	if ladder, _ := ladder.Store.Matches(MatchFilter{Kind: KindLadder}); len(ladder) != 0 {
		t.Errorf("expected no ladder match: %+v", ladder)
	}
}

func TestAbortManualMatch(t *testing.T) {
	player := &recordingPlayer{holdManual: true}
	ladder := newTestLadder(t, testBots(2), player)
	settings := ladder.Settings.Get()
	settings.Paused = true
	ladder.Settings = NewLiveSettings("", settings)
	ladder.Manual = NewManualGame()

	// Not picked up yet: aborted at once
	game := NewManualGame()
	game.Start(testManualMatch(t))
	if !game.Abort() || manualStatus(game) != ManualAborted {
		t.Error("a match not started yet should be aborted at once")
	}

	ladder.Manual.Start(testManualMatch(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ladder.Serve(ctx, nil)

	waitFor(t, "the match to play", func() bool { return manualStatus(ladder.Manual) == ManualPlaying })
	if ladder.Manual.Start(testManualMatch(t)) == nil {
		t.Error("only one manual match at a time")
	}
	if ladder.Manual.Close() {
		t.Error("a match being played should not close")
	}
	if !ladder.Manual.Abort() {
		t.Error("a match being played should be aborted")
	}
	waitFor(t, "the abort", func() bool { return manualStatus(ladder.Manual) == ManualAborted })
	if ladder.Manual.Abort() {
		t.Error("a match that is over cannot be aborted")
	}
	if matches, _ := ladder.Store.Matches(MatchFilter{}); len(matches) != 0 {
		t.Errorf("aborted matches should not be recorded: %+v", matches)
	}
	// Over: another one can start right away
	if err := ladder.Manual.Start(testManualMatch(t)); err != nil {
		t.Errorf("a new match should start once the last one is over: %v", err)
	}
}

func TestNewManualMatch(t *testing.T) {
	bot := func(team string) ManualBot {
		return ManualBot{TeamVersion: TeamVersion{Team: team, Image: "img", Commit: "c"}}
	}
	for _, teams := range [][]string{{"a"}, {"a", "b", "c", "d", "e"}, {"a", "a"}} {
		var bots []ManualBot
		for _, team := range teams {
			bots = append(bots, bot(team))
		}
		if _, err := NewManualMatch("m", bots, nil); err == nil {
			t.Errorf("%v should not be a valid match", teams)
		}
	}
	for _, n := range []int{2, 3, 4, 6} {
		var bots []ManualBot
		for i := range n {
			bots = append(bots, bot(string(rune('a'+i))))
		}
		match, err := NewManualMatch("m", bots, nil)
		if err != nil || len(match.match().Bots) != n || !match.match().RealTime {
			t.Errorf("%d teams should make a real-time match: %v", n, err)
		}
	}
}

func TestLiveGame(t *testing.T) {
	live := NewLiveGame(map[string]string{"a": "Équipe A"})
	live.Start("g", "m", time.Now(), []string{"a", "b"})
	live.Add([]byte(`{"turn":0}`))
	live.Add([]byte(`{"turn":0}`)) // already there
	live.Add([]byte(`not json`))
	live.Add([]byte(`{"turn":1}`))
	states := live.Since(1)
	if states.From != 1 || len(states.States) != 1 || string(states.States[0]) != `{"turn":1}` || states.Over {
		t.Errorf("unexpected states: %+v", states)
	}
	if states.Players[0] != "Équipe A" || states.Players[1] != "b" {
		t.Errorf("expected the names of the teams (or their identifiers): %v", states.Players)
	}
	live.End()
	if states := live.Since(99); len(states.States) != 0 || !states.Over || states.States == nil {
		t.Errorf("unexpected states past the end: %+v", states)
	}
}

func TestVersionsAt(t *testing.T) {
	store := newTestStore(t)
	t0 := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	build := func(commit string, status BuildStatus, at time.Time) {
		store.RecordBuild(&Build{Team: "a", Tag: "tag-" + commit, Commit: commit, Image: "img-" + commit, Status: status, StartedAt: at, EndedAt: at}, "")
	}
	build("c1", BuildOK, t0)
	build("c2", BuildOK, t0.Add(time.Hour))
	build("c3", BuildFailed, t0.Add(2*time.Hour))
	// c1's image was pruned and built again later: it is still the older version
	build("c1", BuildOK, t0.Add(3*time.Hour))

	at := func(t time.Time) string {
		versions, err := store.VersionsAt(t)
		if err != nil {
			panic(err)
		}
		return versions["a"].Commit
	}
	if v := at(t0.Add(-time.Minute)); v != "" {
		t.Errorf("no version before the first build, got %q", v)
	}
	if v := at(t0.Add(30 * time.Minute)); v != "c1" {
		t.Errorf("expected c1, got %q", v)
	}
	if v := at(t0.Add(5 * time.Hour)); v != "c2" {
		t.Errorf("expected c2 (c3 failed, c1 is older), got %q", v)
	}
	versions, _ := store.VersionsAt(t0.Add(5 * time.Hour))
	if v := versions["a"]; v.Tag != "tag-c2" || v.Image != "img-c2" || !v.Since.Equal(t0.Add(time.Hour)) {
		t.Errorf("unexpected version: %+v", v)
	}
}

func TestManualMatchFiles(t *testing.T) {
	store := newTestStore(t)
	bots := testBots(2)
	manual, _ := store.RecordMatch(MatchInfo{Kind: KindManual}, fakeResult("m", bots, []uint{1, 0}, []BotStatus{Finished, Crashed}))
	for range 3 {
		store.RecordMatch(MatchInfo{}, fakeResult("m", bots, []uint{1, 0}, []BotStatus{Finished, Crashed}))
	}
	// Ladder matches only: bot1 failed its last 3, not the manual one
	if n, _ := store.ConsecutiveFailures("bot1", "c"); n != 3 {
		t.Errorf("expected 3 failures, got %d", n)
	}
	store.PruneFiles(1)
	if _, err := os.Stat(store.ReportPath(manual)); err != nil {
		t.Errorf("manual matches should keep their files: %v", err)
	}
	if matches, _ := store.Matches(MatchFilter{Kind: KindManual}); len(matches) != 1 || matches[0].ID != manual {
		t.Errorf("expected the manual match: %+v", matches)
	}
	// Low disk: ladder matches lose their files first
	store.DeleteOldestFiles(1)
	if _, err := os.Stat(store.ReportPath(manual)); err != nil {
		t.Errorf("manual matches should lose their files last: %v", err)
	}
}

func TestLiveStatesCompressed(t *testing.T) {
	config := &Config{Timezone: "UTC"}
	config.Web.AdminPassword = "secret"
	web, err := NewWeb(config, newTestStore(t))
	if err != nil {
		t.Fatal(err)
	}
	web.Manual = NewManualGame()
	match := testManualMatch(t)
	match.live = NewLiveGame(nil)
	match.live.Start("g", "m", time.Now(), []string{"bot0", "bot1"})
	match.live.Add([]byte(`{"turn":0}`))
	web.Manual.Start(match)
	server := httptest.NewServer(web.Handler())
	defer server.Close()

	// The Go client asks for gzip and decompresses transparently
	req, _ := http.NewRequest("GET", server.URL+"/live/states?game=g", nil)
	req.SetBasicAuth("admin", "secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !res.Uncompressed || !strings.Contains(string(body), `"states":[{"turn":0}]`) {
		t.Errorf("expected compressed states: %v %s", res.Uncompressed, body)
	}
}

func TestAdminPages(t *testing.T) {
	store := newTestStore(t)
	config := &Config{Timezone: "UTC", Ladder: testSettings(Format{"random", 2})}
	config.Web.AdminPassword = "secret"
	now := time.Now()
	for _, team := range []string{"alpha", "beta", "gamma"} {
		store.RecordBuild(&Build{Team: team, Tag: "v1", Commit: team + "-0123456789", Image: "img", Status: BuildOK, StartedAt: now, EndedAt: now}, "")
	}
	store.SavePoolState(&PoolState{Teams: []TeamState{
		{Team: Team{ID: "alpha", Name: "Alpha 🐝"}}, {Team: Team{ID: "beta", Name: "Bêta"}}, {Team: Team{ID: "gamma", Name: "Gamma"}},
		{Team: Team{ID: "delta", Name: "Delta"}, Error: "build failed"},
	}})

	web, err := NewWeb(config, store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(web.Handler())
	defer server.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	password := "secret"
	var cookies []*http.Cookie
	request := func(method string, path string, form url.Values) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if password != "" {
			req.SetBasicAuth("admin", password)
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		cookies = append(cookies, res.Cookies()...)
		return res
	}
	get := func(path string, status int, contains ...string) {
		t.Helper()
		res := request("GET", path, nil)
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
	post := func(path string, form url.Values, location string) {
		t.Helper()
		res := request("POST", path, form)
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), location) {
			t.Errorf("%s %v: expected a redirection to %s, got %d %s", path, form, location, res.StatusCode, res.Header.Get("Location"))
		}
	}

	// The operator's pages ask for the password; the public ones do not
	password = ""
	get("/", 200)
	if strings.Contains(fetch(t, server.URL+"/"), `href="/live"`) {
		t.Error("no link to the operator's page for visitors")
	}
	get("/live", 401)
	get("/live/status", 401)
	if res := request("POST", "/live/abort", nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST without the password: expected 401, got %d", res.StatusCode)
	}
	password = "wrong"
	get("/live", 401)
	password = "secret"

	// Without the ladder in this process: shown, but not playable
	get("/live", 200, "Alpha 🐝", "Bêta", "v1 (alpha-01)", "does not run in this process", "no working version")
	post("/live", url.Values{"team": {"alpha", "beta"}}, "/live?error=")
	get("/live/status", 200, "null")
	get("/live/states", 404)

	web.Manual = NewManualGame()
	get("/live", 200, "The ladder is not paused")
	// Versions at a time: none before the builds
	before := now.Add(-time.Hour).UTC().Format("2006-01-02T15:04")
	get("/live?at="+url.QueryEscape(before), 200, "Latest versions")
	post("/live", url.Values{"team": {"alpha"}}, "/live?error=")
	post("/live", url.Values{"team": {"alpha", "delta"}}, "/live?error=")
	post("/live", url.Values{"team": {"alpha", "nobody"}}, "/live?error=")
	post("/live", url.Values{"team": {"alpha", "beta"}, "at": {before}}, "/live?at=")
	post("/live", url.Values{"team": {"gamma", "alpha", "beta"}, "map": {"random"}}, "/live")

	m, _ := web.Manual.Current()
	if m == nil || len(m.Bots) != 3 || m.Status != ManualStarting || m.Map != "random" || m.Bots[0].Name != "Alpha 🐝" {
		t.Fatalf("expected a 3-player match, in the order of the ladder: %+v", m)
	}
	// The page is the live view until the match is closed
	get("/live", 200, "/live/status", "Abort match")
	get("/live/status", 200, `"status":"starting"`, `"name":"Alpha 🐝"`)
	post("/live", url.Values{"team": {"alpha", "beta"}}, "/live?error=")
	post("/live/close", nil, "/live")
	if m, _ := web.Manual.Current(); m == nil {
		t.Error("a match that is not over should not close")
	}
	post("/live/abort", nil, "/live")
	get("/live/status", 200, `"status":"aborted"`)
	post("/live/close", nil, "/live")
	get("/live", 200, "Choose the teams")

	// Once authenticated, the public pages link to the operator's page
	get("/", 200, `href="/live"`, ">Live</a>")

	// Requests from other sites are refused (the browser sends the password anyway)
	req, _ := http.NewRequest("POST", server.URL+"/live", strings.NewReader(url.Values{"team": {"alpha", "beta"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.SetBasicAuth("admin", "secret")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("a cross-site request should be refused, got %d", res.StatusCode)
	}
	if m, _ := web.Manual.Current(); m != nil {
		t.Errorf("a cross-site request should not start a match: %+v", m)
	}

	// Without a password in the configuration, the operator's pages are disabled
	config.Web.AdminPassword = ""
	get("/live", 404)
}

func fetch(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return string(body)
}
