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
  ladder [-config ladder.json]
      Run the ladder: matches, one at a time, until every team has played
      its quota, and the website. The "ladder" settings of the
      configuration are re-read before every match. Send SIGUSR1 to the
      process to rebuild the pool now.

Other commands, for debugging and testing. build builds images: it refuses
to run while the ladder does.

  ladder version
      Print the git commit the ladder was built from
  ladder check [-config ladder.json]
      Check the configuration, and print the address of the website and the
      data directory
  ladder web [-config ladder.json]
      Run the website only
  ladder leaderboard [-config ladder.json] [-at "2006-01-02 15:04"]
      Print the global ladder, now or at the given time (in the configured
      time zone), with the version each team had then
  ladder build [-config ladder.json]
      Fetch the registered teams, build and smoke-test their new versions
  ladder rebuild [-config ladder.json] <team>
      Forget the failed build of a team's current version, so it is built again
  ladder match [-server-image IMAGE] [-cpu-per-bot N] [-report FILE] [-logs DIR] <map> <name>=<image>...
      Play one match in containers with the given images, and print its
      result as JSON (nothing is recorded)`)
	os.Exit(2)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Without a command, run the ladder
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") && os.Args[1] != "-h" && os.Args[1] != "-help" && os.Args[1] != "--help" {
		cmdServe(ctx, os.Args[1:])
		return
	}

	switch os.Args[1] {
	case "version":
		fmt.Println(gitRevision())
	case "check":
		cmdCheck(os.Args[2:])
	case "web":
		cmdWeb(ctx, os.Args[2:])
	case "leaderboard":
		cmdLeaderboard(os.Args[2:])
	case "build":
		cmdBuild(ctx, os.Args[2:])
	case "rebuild":
		cmdRebuild(os.Args[2:])
	case "match":
		cmdMatch(ctx, os.Args[2:])
	default:
		usage()
	}
}

func cmdMatch(ctx context.Context, args []string) {
	config := DefaultRunnerConfig()

	flags := flag.NewFlagSet("match", flag.ExitOnError)
	flags.StringVar(&config.ServerImage, "server-image", config.ServerImage, "docker image of the arena server")
	flags.Float64Var(&config.CPUPerBot, "cpu-per-bot", config.CPUPerBot, "CPU cores per bot")
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
	if result == nil {
		log.Fatal(err)
	}
	defer result.RemoveReport()

	if *report != "" && result.ReportFile != "" {
		data, err := readGzip(result.ReportFile)
		if err == nil {
			err = os.WriteFile(*report, data, 0o644)
		}
		if err != nil {
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

	result.ReportFile = ""
	for i := range result.Bots {
		result.Bots[i].Log = fmt.Sprintf("(%d bytes)", len(result.Bots[i].Log))
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
	if err != nil {
		result.RemoveReport()
		log.Fatal(err)
	}
}

// mustLock takes the lock of the data directory, or exits
func mustLock(ladder *Ladder) {
	if err := lockDataDir(ladder.Config.DataDir); err != nil {
		log.Fatal(err)
	}
}

// openLadder parses the common flags, loads the configuration and opens the store
func openLadder(name string, args []string) (*Ladder, *flag.FlagSet) {
	return openLadderWithFlags(flag.NewFlagSet(name, flag.ExitOnError), args)
}

func openLadderWithFlags(flags *flag.FlagSet, args []string) (*Ladder, *flag.FlagSet) {
	path := flags.String("config", "ladder.json", "configuration file")
	flags.Parse(args)

	log.Printf("Hive Arena ladder, git revision %s", gitRevision())

	config, err := LoadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	store, err := OpenStore(config.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	LogDiskWarning(config)

	runnerConfig := config.RunnerConfig()
	runnerConfig.TempDir = store.TempDir()
	smoke := runnerConfig
	builder := &Builder{
		Store:        store,
		DataDir:      config.DataDir,
		Smoke:        &Runner{Config: smoke},
		BuildTimeout: config.Build.Timeout.Duration,
		KeepImages:   config.Build.KeepImages,
		MaxImageSize: config.Build.MaxImageMB << 20,
		// This machine is dedicated to the ladder
		IsolateCaches: true,
		CacheLimit:    config.Build.CacheLimit,
	}

	ladder := &Ladder{
		Config:   config,
		Settings: NewLiveSettings(*path, config.Ladder),
		Store:    store,
		Player:   &Runner{Config: runnerConfig},
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
		// Only one ladder process plays at a time (see mustLock)
		RemoveLeftovers: RemoveLeftovers,
	}
	pool := &PoolResolver{
		Registry: config.Registry,
		DataDir:  config.DataDir,
		Store:    store,
		Builder:  builder,
	}
	ladder.Pool = func(ctx context.Context) ([]Bot, error) {
		pool.MaxFailures = ladder.Settings.Get().MaxFailures
		return pool.Resolve(ctx)
	}
	return ladder, flags
}

func cmdServe(ctx context.Context, args []string) {
	ladder, _ := openLadder("ladder", args)
	defer ladder.Store.Close()
	mustLock(ladder)

	wake := make(chan struct{}, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		for range signals {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()

	go serveWeb(ladder)

	if err := ladder.Serve(ctx, wake); err != nil {
		log.Fatal(err)
	}
}

func serveWeb(ladder *Ladder) {
	web, err := NewWeb(ladder.Config, ladder.Store)
	if err != nil {
		log.Fatal(err)
	}
	web.Settings = ladder.Settings
	log.Printf("Website on %s", ladder.Config.Web.Listen)
	// No write timeout: backups and reports can take a while to download
	server := &http.Server{
		Addr:              ladder.Config.Web.Listen,
		Handler:           web.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Fatal(server.ListenAndServe())
}

func cmdWeb(ctx context.Context, args []string) {
	ladder, _ := openLadder("web", args)
	defer ladder.Store.Close()
	go serveWeb(ladder)
	<-ctx.Done()
}

func cmdLeaderboard(args []string) {
	flags := flag.NewFlagSet("leaderboard", flag.ExitOnError)
	at := flags.String("at", "", `the ladder at this time ("2006-01-02 15:04", default: now)`)
	ladder, _ := openLadderWithFlags(flags, args)
	defer ladder.Store.Close()

	var before time.Time
	if *at != "" {
		loc, _ := ladder.Config.Location()
		var err error
		if before, err = time.ParseInLocation("2006-01-02 15:04", *at, loc); err != nil {
			log.Fatalf("invalid time %q (expected \"2006-01-02 15:04\")", *at)
		}
	}
	matches, err := ladder.Store.RatedMatches(before)
	if err != nil {
		log.Fatal(err)
	}
	ratings := ComputeRatings(ladder.Settings.Get().Rating, matches)

	when := "now"
	if !before.IsZero() {
		when = "at " + before.Format("2006-01-02 15:04 MST")
	}
	fmt.Printf("Global ladder %s, over %d matches\n\n", when, len(matches))
	fmt.Printf("%-4s %-24s %8s %8s %8s %8s  %s\n", "#", "team", "rating", "mu", "sigma", "matches", "commit")
	for i, t := range ratings.Global.Standings() {
		fmt.Printf("%-4d %-24s %8.2f %8.2f %8.2f %8d  %s\n", i+1, t.Team, t.Score, t.Rating.Mu, t.Rating.Sigma, t.Games, t.Commit)
	}
}

func cmdBuild(ctx context.Context, args []string) {
	ladder, _ := openLadder("build", args)
	defer ladder.Store.Close()
	mustLock(ladder)

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
	fmt.Printf("Forgot build %d (%s, %s): it will be built again at the next build check\n", builds[0].ID, builds[0].Tag, builds[0].Status)
}

func cmdCheck(args []string) {
	flags := flag.NewFlagSet("check", flag.ExitOnError)
	path := flags.String("config", "ladder.json", "configuration file")
	flags.Parse(args)

	config, err := LoadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	dataDir, err := filepath.Abs(config.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("listen %s\ndataDir %s\n", config.Web.Listen, dataDir)
}
