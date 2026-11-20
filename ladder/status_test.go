package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStatusAndHealth(t *testing.T) {
	store := newTestStore(t)
	config := &Config{DataDir: store.dir, Timezone: "UTC", Ladder: testSettings(Format{"m", 2}, Format{"m", 3})}

	a, b := Bot{Name: "a"}, Bot{Name: "b"}
	store.RecordMatch(MatchInfo{}, fakeResult("m", []Bot{a, b}, []uint{1, 0}, []BotStatus{Finished, Finished}))
	store.SavePoolState(&PoolState{Teams: []TeamState{{Team: Team{Name: "a"}, Bot: &a}, {Team: Team{Name: "b"}, Error: "build-failed"}}})

	web, err := NewWeb(config, store)
	if err != nil {
		t.Fatal(err)
	}
	// Pretend docker was just checked and works
	web.docker.checked = time.Now()

	server := httptest.NewServer(web.Handler())
	defer server.Close()

	get := func(path string) (int, string) {
		res, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}

	code, body := get("/health")
	if code != 200 || strings.TrimSpace(body) != "ok" {
		t.Errorf("expected a healthy ladder, got %d: %s", code, body)
	}

	_, body = get("/api/status")
	var status Status
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatal(err)
	}
	// One team plays: no format can be played
	if !status.Healthy || status.Database.Matches != 1 || status.Matchmaker.Last24h.Matches != 1 || status.Matchmaker.Demand != 0 ||
		status.Teams.Registered != 2 || status.Teams.Playing != 1 || len(status.Teams.Errors) != 1 || status.Disk.Total == 0 {
		t.Errorf("unexpected status: %s", body)
	}

	// Low disk space
	config.MinFreeDiskGB = 1e9
	code, body = get("/health")
	if code != 503 || !strings.Contains(body, "low disk space") {
		t.Errorf("expected a low disk warning, got %d: %s", code, body)
	}
	config.MinFreeDiskGB = 0

	// A stuck matchmaker
	heartbeat.Store(time.Now().Add(-time.Hour))
	defer heartbeat.Store(time.Time{})
	code, body = get("/health")
	if code != 503 || !strings.Contains(body, "stuck") {
		t.Errorf("expected a stuck matchmaker warning, got %d: %s", code, body)
	}
}

func TestDemand(t *testing.T) {
	settings := testSettings(Format{"a", 2}, Format{"b", 4}, Format{"a", 2}, Format{"c", 6})
	settings.Quota = 20
	// 10 teams: 100 matches of 2 players, 50 of 4, 33 of 6
	if d := demand(settings, 10); d != 183 {
		t.Errorf("expected 183 matches per day, got %d", d)
	}
	if d := demand(settings, 4); d != 60 {
		t.Errorf("expected 60 matches per day without the 6-player format, got %d", d)
	}
}
