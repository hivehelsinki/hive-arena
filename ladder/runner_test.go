package main

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// Builds the images used by the runner tests. Skips the test if docker is not available.
func requireDocker(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("docker tests are skipped in short mode")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker is not available")
	}

	builds := [][]string{
		{"build", "-q", "-t", "hive-arena-server", ".."},
		{"build", "-q", "-t", "example-agent-go", "-f", "../example-agent-go/Dockerfile", ".."},
		{"build", "-q", "-t", "hive-ladder-crashbot", "testdata/crashbot"},
	}
	for _, args := range builds {
		if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
			t.Fatalf("docker %v: %s\n%s", args, err, out)
		}
	}
}

func TestRunMatch(t *testing.T) {
	requireDocker(t)

	config := DefaultRunnerConfig()
	config.JoinTimeout = 20 * time.Second
	config.MatchTimeout = 5 * time.Minute
	runner := Runner{Config: config}

	match := Match{Map: "tiny", Bots: []Bot{
		{Name: "good", Image: "example-agent-go"},
		{Name: "crash", Image: "hive-ladder-crashbot"},
		{Name: "nojoin", Image: "busybox"},
		{Name: "missing", Image: "hive-ladder-no-such-image"},
	}}

	// Slow: once the crashing bot is gone, each turn waits for the turn timeout
	result, err := runner.Run(context.Background(), match)
	if err != nil {
		t.Fatal(err)
	}

	if result.Result == nil || len(result.Result.Players) != 4 || len(result.Report) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	expected := map[string]BotStatus{"good": Finished, "crash": Crashed, "nojoin": NoJoin, "missing": NoJoin}
	seats := make(map[int]bool)
	for _, bot := range result.Bots {
		if bot.Status != expected[bot.Name] {
			t.Errorf("%s: expected status %s, got %s", bot.Name, expected[bot.Name], bot.Status)
		}
		if bot.Seat < 0 || result.Result.Players[bot.Seat].Name != bot.Name {
			t.Errorf("%s: wrong seat %d", bot.Name, bot.Seat)
		}
		seats[bot.Seat] = true
	}
	if len(seats) != 4 {
		t.Errorf("expected 4 different seats, got %v", seats)
	}

	for _, bot := range result.Bots {
		if bot.Name == "crash" && bot.ExitCode != 3 {
			t.Errorf("expected the exit code of the crashing bot, got %d", bot.ExitCode)
		}
		if bot.Name == "good" && bot.Log == "" {
			t.Error("expected the log of the good bot")
		}
	}

	// Everything is cleaned up
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+DockerLabel).Output()
	if len(out) != 0 {
		t.Errorf("containers were left behind: %s", out)
	}
}

func TestNoJoinDoesNotWaitForTimeout(t *testing.T) {
	requireDocker(t)

	config := DefaultRunnerConfig()
	config.JoinTimeout = 30 * time.Second
	runner := Runner{Config: config}

	start := time.Now()
	result, err := runner.Run(context.Background(), Match{Map: "tiny", Bots: []Bot{
		{Name: "good", Image: "example-agent-go"},
		{Name: "nojoin", Image: "busybox"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > config.JoinTimeout {
		t.Errorf("a bot that exited should not make the runner wait for the join timeout")
	}
	if result.Bots[1].Status != NoJoin {
		t.Errorf("unexpected outcome: %+v", result.Bots[1])
	}
}

func TestValidateMatch(t *testing.T) {
	if validateMatch(Match{Map: "tiny"}) == nil {
		t.Error("a match without bots should be invalid")
	}
	if validateMatch(Match{Map: "tiny", Bots: []Bot{{Name: "a", Image: "x"}, {Name: "a", Image: "y"}}}) == nil {
		t.Error("duplicate names should be invalid")
	}
	if validateMatch(Match{Map: "tiny", Bots: []Bot{{Name: "a", Image: "x"}, {Name: "b", Image: "y"}}}) != nil {
		t.Error("valid match rejected")
	}
}
