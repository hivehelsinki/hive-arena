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
	config := &Config{DataDir: store.dir, Schedule: Schedule{Times: []string{"08:00"}, Timezone: "UTC"}}

	a, b := Bot{Name: "a"}, Bot{Name: "b"}
	run, _ := store.CreateRun("ladder", nil, []Bot{a, b})
	store.RecordMatch(MatchInfo{Kind: LadderMatch, RunID: run}, fakeResult("m", []Bot{a, b}, []uint{1, 0}, []BotStatus{Finished, Finished}))
	store.FinishRun(run, RunComplete)
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
	if !status.Healthy || status.Database.Matches != 1 || status.LastRun == nil || status.LastRun.ID != run ||
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

	// Failed run
	failed, _ := store.CreateRun("ladder", nil, nil)
	store.FinishRun(failed, RunFailed)
	code, body = get("/health")
	if code != 503 || !strings.Contains(body, "failed") {
		t.Errorf("expected a failed run warning, got %d: %s", code, body)
	}
}
