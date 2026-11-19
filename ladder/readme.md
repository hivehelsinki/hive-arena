# Hive Arena ladder

The ladder service: plays registered bots against each other in Docker containers, records the results, and publishes them. See [LADDER.md](../LADDER.md) for the design.

To deploy it on a VM, or update it there: `sudo ladder/deploy/setup.sh` from the repository (see [deploy/setup.md](deploy/setup.md)).

## Running the ladder

Copy [ladder.example.json](ladder.example.json) to `ladder.json` and adjust it, then run `go run ./ladder` (or `ladder -config path/to/ladder.json`) from the repository root. It plays ladder matches one at a time until every team has played its quota (see below), builds new versions every `build.interval`, and serves the website. The `ladder` section of the configuration is re-read before every match, so the quota, formats, ratings or `paused` can change while it runs. Send `SIGUSR1` to the process to build new versions right away.

Everything is derived from the recorded matches and the configuration: after a restart, the ladder resumes where it left off (the match being played when it stopped is lost).

The other commands are for debugging and testing (`ladder help` lists them). `build` builds images: it takes a lock on the data directory, and refuses to start while the ladder service runs.

- `go run ./ladder web`: runs the website only
- `go run ./ladder leaderboard [-at "2026-12-04 16:00"]`: prints the global ladder, now or at a given time (in the configured `timezone`), with the version each team had then
- `go run ./ladder build`: fetches the registered teams, builds and smoke-tests their new versions (also done every `build.interval` by the ladder)
- `go run ./ladder rebuild <team>`: forgets the failed build of a team's current version, so that it is built again (for instance after a network failure during the build)
- `go run ./ladder match`: plays one match with the given images, without recording anything (see below)

Configuration (`ladder.json`):

- `dataDir`: where the database, reports and logs are kept
- `timezone`: of the times shown on the website
- `ladder`: re-read before every match.
  - `formats`: the formats played, each with its own ladder: `{"map": "random", "players": N}`, a new random map for each match, for 2, 3, 4 or 6 players
  - `quota`: matches each team plays per 24 hours (a rolling window) in each format; every match counts, challenged or challenging
  - `window`: opponents are drawn among this many teams ranked just above the challenger (at least players − 1; below, for the teams at the top)
  - `randomShare`: share of the matches against random opponents instead
  - `maxFailures`: a bot that fails (crashes or doesn't join) this many matches in a row with the same commit is left out, until its commit changes
  - `keepMatchFiles`: each team keeps the report and logs of its last matches; older ones are deleted (results are kept forever)
  - `headToHeadPeriod`: head-to-head results are computed over this period
  - `paused`: no match is started
  - `rating`: parameters of the ratings (see below); changing them recomputes every rating
- `runner`: server image, CPU cores split between the bots of a match, per-bot memory and process limits, memory of the match server (`serverMemory`: it keeps the whole game in memory), timeouts
- `registry`: where teams are registered: a git repository (`url`, `branch`, `file`), or a local file (`path`)
- `build`: build timeout, number of successful images kept per team, image size limit (`maxImageMB`), size of the docker build cache (`cacheLimit`), how often new versions are built (`interval`, between matches)
- `minFreeDiskGB`: below this free space in `dataDir`, the report and logs of the oldest matches are deleted, a warning is logged and `/health` reports a problem
- `web.listen`: address of the website
- `web.backupToken`: enables the backup route (`/backup?token=`), see [deploy/setup.md](deploy/setup.md)

## Teams and builds

The registry file lists the teams:

```json
[
  { "name": "SuperTeam", "repo": "https://github.com/someone/superteam-bot", "members": ["alice", "bob"] }
]
```

Optional fields: `ref` (a tag, branch or commit to use instead of the latest tag on `main`), `context` and `dockerfile` (paths from the repository root: the build context, default the root, and the Dockerfile, inside the context, default the `Dockerfile` at the top of the context).

Every `build.interval`, between matches, for each team, the ladder updates a mirror of its repository, takes the most recent tag on `main`, and builds it if it is new (`git archive | docker build -`, no checkout). A new image must pass a smoke test: a 1-player game on a random map, played to the end. Builds and their logs are recorded; a failed build is not retried (see `rebuild`), and the team's last good image keeps playing. Failures that are not the team's (interrupted build, docker or network problems) are not recorded, and the build is tried again.

In a registry fetched from git, teams must use `https://` repositories, and invalid entries are left out without affecting the others (see `registryProblems` in `/api/status`). When the registry cannot be fetched or is invalid, its last valid version is used. A local registry file (`registry.path`) accepts any repository.

A team whose bot failed (crashed or did not join) its last `maxFailures` matches with the same commit is left out of the ladder until it pushes a new tag; its page says so.

## Matchmaking and ratings

Before every match, the matchmaker picks the team and format furthest below the quota (matches in the last 24 hours), and draws its opponents among the teams just above it in that format's ladder (or, for `randomShare` of the matches, among all teams). When every team has played its quota, it waits, and looks again every minute.

Ratings follow OpenSkill's Plackett-Luce model (TrueSkill-style: a skill estimate μ and an uncertainty σ per team), with ties, globally and per format. Teams are ranked by μ − 3σ, so a new team starts at 0. `rating.tau` adds uncertainty before every match, so that ratings follow teams that improve; when a team's version changes, its σ is raised to at least `rating.versionSigma`. Ratings are recomputed from all the valid ladder matches every time, so they can be computed at any past time. See [LADDER.md](../LADDER.md) for the reasons.

Matches are played one at a time. Each bot joins through the private ID of its own seat, so it always plays under its team's name. A crashed bot's seat is forfeited (the server stops waiting for its orders), so it does not slow the game down. A match that fails (timeout, server error) is recorded as void, with the error and the bots' output.

## Playing one match

```sh
# Images of the server and of the bots
docker build -t hive-arena-server .
docker build -t example-agent-go -f example-agent-go/Dockerfile .
docker build -t example-agent-lua example-agent-lua

go run ./ladder match [-report report.json] [-logs dir] random a=example-agent-go b=example-agent-lua
```

Each bot is given as `name=image`. The runner creates a private network, starts a server and the bots (with the resource limits of the ladder), waits for the end of the game, and prints the outcome of each bot (and, for a failed match, the error with what is known):

- `finished`: played until the end
- `crashed`: joined, but exited before the end of the game
- `no-join`: did not join in time (or could not be started); a stand-in played its seat, posting no orders

Containers and networks are always removed at the end. All of them have the `hive-ladder` label.

## Tests

`go test ./ladder` runs the runner tests with Docker (skipped with `-short` or when Docker is unavailable). 

## Website

Read-only, no accounts: the global ladder and one per format, the head-to-head table, matches (filterable by team and map), team pages with their ratings, their matches in the last 24 hours against the quota, builds and their logs, match pages with bot outputs and report downloads (until they expire). The same data is available as JSON under `/api/` (`leaderboard`, `matches`, `teams`).

`/screen` is meant for a big screen: the leaderboard, and the replays of the latest matches, one after the other.

Replays open in the browser version of the viewer, served at `/viewer/` from `web.viewerDir` (default `viewer/web`, relative to the working directory). Build it first with `viewer/build-web.sh`.

## Status and backups

- `/health`: `ok`, or the list of problems with status 503 (for monitoring)
- `/api/status`: disk space, Docker, database, what the ladder is doing (`activity`), the matchmaker (paused, last error, matches in the last 24 hours against the quotas' demand, configuration warnings), teams with problems, registry problems
- `/backup?token=`: a tar archive named after the time, with a consistent snapshot of the database and the build logs (not the match reports and logs, which expire), to be fetched by a backup system elsewhere. See [deploy/setup.md](deploy/setup.md).
