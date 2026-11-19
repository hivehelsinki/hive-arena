package main

import (
	"context"
	"os/exec"
	"strings"
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
		{"build", "-q", "-t", "hive-ladder-slowcrashbot", "testdata/slowcrashbot"},
		{"build", "-q", "-t", "hive-ladder-nojoinbot", "testdata/nojoinbot"},
		{"build", "-q", "-t", "hive-ladder-impostorbot", "testdata/impostorbot"},
		{"build", "-q", "-t", "hive-ladder-idlebot", "testdata/idlebot"},
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

	match := Match{Map: "random", Bots: []Bot{
		{Name: "good", Image: "example-agent-go"},
		{Name: "crash", Image: "hive-ladder-crashbot"},
		{Name: "nojoin", Image: "busybox"},
		{Name: "missing", Image: "hive-ladder-no-such-image"},
	}}

	result, err := runner.Run(context.Background(), match)
	if err != nil {
		t.Fatal(err)
	}

	defer result.RemoveReport()
	if result.Result == nil || len(result.Result.Players) != 4 || result.ReportFile == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if report, err := readGzip(result.ReportFile); err != nil || !strings.Contains(string(report), `"history"`) {
		t.Errorf("expected the report in %s: %s", result.ReportFile, err)
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

	if crash := result.Bots[1]; !result.Result.Players[crash.Seat].Forfeited {
		t.Error("the crashed bot should have been forfeited")
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
	result, err := runner.Run(context.Background(), Match{Map: "random", Bots: []Bot{
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

// Bots that exit before the game starts are forfeited once it does, so that
// the server does not wait for their orders every turn
func TestEarlyCrashesAreForfeited(t *testing.T) {
	requireDocker(t)

	config := DefaultRunnerConfig()
	config.JoinTimeout = 30 * time.Second
	runner := Runner{Config: config}

	start := time.Now()
	result, err := runner.Run(context.Background(), Match{Map: "random", Bots: []Bot{
		{Name: "good", Image: "example-agent-go"},
		{Name: "crash", Image: "hive-ladder-crashbot"},
		{Name: "slowcrash", Image: "hive-ladder-slowcrashbot"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer result.RemoveReport()

	for _, bot := range result.Bots[1:] {
		if bot.Status != Crashed || !result.Result.Players[bot.Seat].Forfeited {
			t.Errorf("%s should have crashed and been forfeited: %+v", bot.Name, bot)
		}
	}
	// Without forfeits, each of the 50+ turns would wait for the 2 s turn timeout
	if elapsed := time.Since(start); elapsed > 60*time.Second {
		t.Errorf("the match took %s: the crashed bots were not forfeited", elapsed)
	}
}

// A bot always plays under its own name, whatever name it joins with
func TestBotsJoinUnderTheirOwnName(t *testing.T) {
	requireDocker(t)

	runner := Runner{Config: DefaultRunnerConfig()}
	result, err := runner.Run(context.Background(), Match{Map: "random", Bots: []Bot{
		{Name: "good", Image: "example-agent-go"},
		{Name: "honest", Image: "hive-ladder-impostorbot"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer result.RemoveReport()

	bot := result.Bots[1]
	if bot.Seat < 0 || result.Result.Players[bot.Seat].Name != "honest" {
		t.Errorf("the bot should play under its own name: %+v, players %+v", bot, result.Result.Players)
	}
}

// The output of a bot that did not join is kept: it usually says why
func TestNoJoinOutputIsKept(t *testing.T) {
	requireDocker(t)

	runner := Runner{Config: DefaultRunnerConfig()}
	result, err := runner.Run(context.Background(), Match{Map: "random", Bots: []Bot{
		{Name: "good", Image: "example-agent-go"},
		{Name: "broken", Image: "hive-ladder-nojoinbot"},
		{Name: "missing", Image: "hive-ladder-no-such-image"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer result.RemoveReport()

	if bot := result.Bots[1]; bot.Status != NoJoin || !strings.Contains(bot.Log, "no configuration found") {
		t.Errorf("expected the output of the bot that did not join: %+v", bot)
	}
	if bot := result.Bots[2]; bot.Status != NoJoin || !strings.Contains(bot.Log, "could not be started") {
		t.Errorf("expected the reason why the bot could not be started: %+v", bot)
	}
}

// A failed match returns what is known about the bots
func TestFailedMatchHasPartialResult(t *testing.T) {
	requireDocker(t)

	config := DefaultRunnerConfig()
	config.MatchTimeout = 15 * time.Second
	runner := Runner{Config: config}

	// The idle bot makes every turn wait for the turn timeout
	result, err := runner.Run(context.Background(), Match{Map: "random", Bots: []Bot{
		{Name: "good", Image: "example-agent-go"},
		{Name: "idle", Image: "hive-ladder-idlebot"},
	}})
	if err == nil {
		result.RemoveReport()
		t.Fatal("the match should have timed out")
	}
	if result == nil || result.Error == "" || len(result.Bots) != 2 || !strings.Contains(result.Bots[1].Log, "joined") {
		t.Fatalf("expected a partial result with the bots' output, got %+v (%s)", result, err)
	}
}

func TestValidateMatch(t *testing.T) {
	if validateMatch(Match{Map: "random"}) == nil {
		t.Error("a match without bots should be invalid")
	}
	if validateMatch(Match{Map: "random", Bots: []Bot{{Name: "a", Image: "x"}, {Name: "a", Image: "y"}}}) == nil {
		t.Error("duplicate names should be invalid")
	}
	if validateMatch(Match{Map: "random", Bots: []Bot{{Name: "a", Image: "x"}, {Name: "b", Image: "y"}}}) != nil {
		t.Error("valid match rejected")
	}
}

func TestBotCPU(t *testing.T) {
	config := RunnerConfig{CPUPerBot: 1, Cores: 5.5}
	for n, want := range map[int]float64{1: 1, 2: 1, 5: 1, 6: 5.5 / 6} {
		if got := botCPU(config, n); got != want {
			t.Errorf("%d bots: expected %.3f cores each, got %.3f", n, want, got)
		}
	}
}
