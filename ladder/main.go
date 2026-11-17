package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  ladder serve [-config ladder.json]
      Run the ladder at the scheduled times, and the website. Send SIGUSR1
      to start a run now.
  ladder web [-config ladder.json]
      Run the website only
  ladder run [-config ladder.json]
      Play one ladder run now, and exit
  ladder leaderboard [-config ladder.json]
      Print the current leaderboard
  ladder build [-config ladder.json]
      Fetch the registered teams, build and smoke-test their new versions
  ladder rebuild [-config ladder.json] <team>
      Forget the failed build of a team's current version, so it is built again`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch os.Args[1] {
	case "match":
		cmdMatch(ctx, os.Args[2:])
	case "serve":
		cmdServe(ctx, os.Args[2:])
	case "web":
		cmdWeb(ctx, os.Args[2:])
	case "run":
		cmdRun(ctx, os.Args[2:])
	case "leaderboard":
		cmdLeaderboard(os.Args[2:])
	case "build":
		cmdBuild(ctx, os.Args[2:])
	case "rebuild":
		cmdRebuild(os.Args[2:])
	default:
		usage()
	}
}

func cmdMatch(ctx context.Context, args []string) {
	config := DefaultRunnerConfig()

	flags := flag.NewFlagSet("match", flag.ExitOnError)
	flags.StringVar(&config.ServerImage, "server-image", config.ServerImage, "docker image of the arena server")
	flags.Float64Var(&config.Cores, "cores", config.Cores, "CPU cores split between the bots")
	report := flags.String("report", "", "file to save the game report in")
	logs := flags.String("logs", "", "directory to save the bot logs in")
	flags.Parse(args)

	if flags.NArg() < 2 {
		usage()
	}

	match := Match{Map: flags.Arg(0)}
	for _, arg := range flags.Args()[1:] {
		name, image, ok := strings.Cut(arg, "=")
		if !ok {
			usage()
		}
		match.Bots = append(match.Bots, Bot{Name: name, Image: image})
	}

	runner := Runner{Config: config}
	result, err := runner.Run(ctx, match)
	if err != nil {
		log.Fatal(err)
	}

	if *report != "" {
		if err := os.WriteFile(*report, result.Report, 0o644); err != nil {
			log.Fatal(err)
		}
	}

	if *logs != "" {
		for _, bot := range result.Bots {
			path := filepath.Join(*logs, bot.Name+".log")
			if err := os.WriteFile(path, []byte(bot.Log), 0o644); err != nil {
				log.Fatal(err)
			}
		}
	}

	result.Report = nil
	for i := range result.Bots {
		result.Bots[i].Log = fmt.Sprintf("(%d bytes)", len(result.Bots[i].Log))
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}

// openLadder parses the common flags, loads the configuration and opens the store
func openLadder(name string, args []string) (*Ladder, *flag.FlagSet) {
	flags := flag.NewFlagSet(name, flag.ExitOnError)
	path := flags.String("config", "ladder.json", "configuration file")
	flags.Parse(args)

	config, err := LoadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	store, err := OpenStore(config.DataDir)
	if err != nil {
		log.Fatal(err)
	}

	smoke := config.RunnerConfig()
	smoke.MatchTimeout = 5 * time.Minute
	builder := &Builder{
		Store:        store,
		DataDir:      config.DataDir,
		Smoke:        &Runner{Config: smoke},
		SmokeMap:     config.Build.SmokeMap,
		BuildTimeout: config.Build.Timeout.Duration,
		KeepImages:   config.Build.KeepImages,
	}

	ladder := &Ladder{
		Config: config,
		Store:  store,
		Player: &Runner{Config: config.RunnerConfig()},
		Rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	ladder.Pool = func(ctx context.Context) ([]Bot, error) {
		teams, err := LoadTeams(ctx, config.Registry, config.DataDir)
		if err != nil {
			return nil, err
		}
		bots, states := builder.Pool(ctx, teams)
		if err := store.SaveTeamStates(states); err != nil {
			return nil, err
		}
		return append(bots, config.Bots...), nil
	}
	return ladder, flags
}

func cmdServe(ctx context.Context, args []string) {
	ladder, _ := openLadder("serve", args)
	defer ladder.Store.Close()

	if err := RemoveLeftovers(ctx); err != nil {
		log.Fatal(err)
	}

	trigger := make(chan struct{}, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		for range signals {
			select {
			case trigger <- struct{}{}:
			default:
			}
		}
	}()

	go serveWeb(ladder)

	if err := ladder.Serve(ctx, trigger); err != nil {
		log.Fatal(err)
	}
}

func serveWeb(ladder *Ladder) {
	web, err := NewWeb(ladder.Config, ladder.Store)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Website on %s", ladder.Config.Web.Listen)
	log.Fatal(http.ListenAndServe(ladder.Config.Web.Listen, web.Handler()))
}

func cmdWeb(ctx context.Context, args []string) {
	ladder, _ := openLadder("web", args)
	defer ladder.Store.Close()
	go serveWeb(ladder)
	<-ctx.Done()
}

func cmdRun(ctx context.Context, args []string) {
	ladder, _ := openLadder("run", args)
	defer ladder.Store.Close()

	if err := RemoveLeftovers(ctx); err != nil {
		log.Fatal(err)
	}
	loc, _ := ladder.Config.Location()
	deadline := ladder.Config.Schedule.NextRunTime(time.Now(), loc).Add(-ladder.Config.DeadlineMargin.Duration)

	if _, err := ladder.RunOnce(ctx, deadline); err != nil {
		log.Fatal(err)
	}
	printLeaderboard(ladder)
}

func cmdLeaderboard(args []string) {
	ladder, _ := openLadder("leaderboard", args)
	defer ladder.Store.Close()
	printLeaderboard(ladder)
}

func printLeaderboard(ladder *Ladder) {
	board, runs, err := ladder.Store.Leaderboard(ladder.Config.Schedule.Window)
	if err != nil {
		log.Fatal(err)
	}

	var ids []string
	for _, run := range runs {
		ids = append(ids, fmt.Sprint(run.ID))
	}
	fmt.Printf("Leaderboard over runs %s\n\n", strings.Join(ids, ", "))
	fmt.Printf("%-4s %-24s %8s %8s %6s  %s\n", "#", "team", "score", "± err", "games", "commit")
	for i, s := range board.Standings {
		fmt.Printf("%-4d %-24s %8.1f %8.1f %6d  %s\n", i+1, s.Team, s.Score.Mean, s.Score.StdErr, s.Score.Games, s.Commit)
	}
}

func cmdBuild(ctx context.Context, args []string) {
	ladder, _ := openLadder("build", args)
	defer ladder.Store.Close()

	bots, err := ladder.Pool(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, bot := range bots {
		fmt.Printf("%-24s %s\n", bot.Name, bot.Image)
	}
}

func cmdRebuild(args []string) {
	ladder, flags := openLadder("rebuild", args)
	defer ladder.Store.Close()
	if flags.NArg() != 1 {
		usage()
	}

	builds, err := ladder.Store.Builds(flags.Arg(0), 1)
	if err != nil {
		log.Fatal(err)
	}
	if len(builds) == 0 || builds[0].Status == BuildOK {
		log.Fatalf("no failed build to forget for %s", flags.Arg(0))
	}
	if err := ladder.Store.DeleteBuild(builds[0].ID); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Forgot build %d (%s, %s): it will be built again at the next run\n", builds[0].ID, builds[0].Tag, builds[0].Status)
}
