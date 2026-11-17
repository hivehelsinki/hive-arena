# Hive Arena Ladder: Plan

A staff-run service that plays all registered bots against each other **twice a day** (for instance at 8:00 and 16:00), records every game, and publishes results and replays on a public website. It gives participants regular automatic feedback during the competition, without anyone having to run matches by hand. The same machinery can also run a quick tournament for a live event.

## Background

In the 2019 Arena, bots were built and run by the organizers, which turned into a mess of language versions, virtual environments and OS differences. In Arena 2025 each team ran its own bot against the server API, which avoided that problem but meant the only interaction between teams was a preview tournament and the final, both needing an organizer present and teams typing in match names on screen. The goal now is for a team to register once, keep pushing, and see fresh results twice a day.

The compromise that keeps environment problems out of the organizers' hands: **teams provide a working Dockerfile**. The organizers only build and run it.

## Scope and principles

- **Staff run.** Only staff operate it, so there are no user accounts, permissions or admin API.
- **Separate service from the game server.** The existing server (`server/`) keeps its current role and API, apart from a few small backward-compatible additions (see "Server changes"), and keeps being used alone for live events. The ladder is its own program.
- **The ladder spawns its own servers and agents via Docker.** Every match gets a fresh server container and one container per bot, on a private Docker network. The ladder never talks to the long-running public server.
- **The public side only displays results.** No login, no upload, no write routes.
- **Registration is a pull request** on a public, organizer-owned repo (or edits by staff).
- **Everything is recorded and replayable**: every game is stored, downloadable, and viewable in the browser.
- **Security is "friendly competition" level**: equal resource limits and no network access beyond the match. Cheating is handled by people, not code.

## Architecture

```
                 registry repo (teams.json, PRs)
                           |
                    +------v-------+        +--------------------+
                    |   ladder     |------->|  Docker daemon     |
                    |  (one Go     |        |  - build images    |
                    |   binary)    |        |  - per match:      |
                    |              |        |    server + N bots |
                    | scheduler    |        |    on private net  |
                    | runner       |        +--------------------+
                    | store (SQLite + files)
                    | web (read-only HTTP)
                    +------+-------+
                           |
          public website: leaderboard, head-to-head, match list,
          report download, in-browser replay (viewer compiled to wasm)
```

One Go binary, `ladder`, with these parts:

1. **Registry sync**: reads the team list and resolves what to run.
2. **Builder**: turns a team's repo at a tag into a Docker image.
3. **Scheduler**: decides which matches to play (full round-robins).
4. **Runner**: plays one match in containers and collects the result.
5. **Store**: SQLite for match records, plus the report files on disk.
6. **Web**: a read-only HTTP site over the store, including the wasm viewer.

It lives in the same repo (`ladder/`, `go run ./ladder`), reuses the `arena` package for types and report parsing, and the server image already built by the existing `Dockerfile`.

## Registration

A public, organizer-owned repo holds `teams.json`:

```json
[
  { "name": "SuperTeam", "repo": "https://github.com/someone/superteam-bot", "members": ["alice", "bob"] }
]
```

- To register or change a team, open a pull request; staff review and merge. Reviewing the PR is the "authentication".
- Team size is limited to 4-5 (checked by staff when merging).
- The ladder pulls the registry repo at the start of each run (and on demand by staff) and picks up changes.
- **What runs:** the most recent tag (by commit date) among the tags that point to a commit on the repo's `main` branch. Tags elsewhere are ignored. Pushing a new tag on `main` is how a team "submits" an update. Images are keyed by commit, so moved or deleted tags are harmless: whatever commit the resolved tag points to at sync time is what runs. An optional `ref` field in the registry pins a specific tag or commit, for staff overrides and for the final.
- Repos must be public (or readable with a staff deploy key); we don't hide bots from each other.

## The bot contract

A team's repo must contain, at its root, a `Dockerfile` that builds an image which, when run, plays one game and exits. The conventions:

- **No arguments, no ports, no volumes.** The image is started with `docker run` using the default `CMD`/`ENTRYPOINT`. The bot only makes outgoing HTTP connections.
- **Configuration is through environment variables**, set by the runner:
  - `ARENA_HOST`: host and port of the match's server (e.g. `server:8080`)
  - `ARENA_GAME_ID`: the game to join
  - `ARENA_NAME`: the name to announce (`/join?name=`)
- The bot joins the game (`/join`), plays until `gameOver` (or until the connection is refused/closed), then exits with status 0.
- No network access other than the match's server; the filesystem is read-only except `/tmp`; the process runs as a non-root user (the runner enforces it, but images should not require root).
- Builds must work offline-free of our help: the build is done on the ladder machine with network access (to fetch dependencies), but under a time limit.
- Dockerfile templates for Go and Lua are added to `example-agent-go` and `example-agent-lua` (mapping the env variables to the programs' arguments), and a short "bot contract" section in the docs. These templates are the reference for other languages.

## Building

- **At the start of each run** (see Scheduling), for every team, resolve the latest tag to a commit; if no image exists for that commit, clone it and `docker build` with a time limit (e.g. 10 minutes) and a size cap.
- Images are tagged `ladder/<team>:<commit>`; old images are pruned (keep the last few per team).
- The build log is stored and shown on the team's page.
- **A failed build never removes a team from the ladder**: the previous good image keeps playing, and the website shows the failure and its log.
- **Smoke test before admission:** a new image must complete a 1-player match on `tiny` (join, play at least one legal turn) before it is used in the ladder. Failure is shown like a build failure. This is the CI/CD-style feedback teams get for free.

## Running a match

For each match the runner:

1. Creates a private Docker network (`--internal`, so nothing in it can reach the outside).
2. Starts a server container from the arena server image on that network, with `--dev` (no minimum turn duration, so games play as fast as the bots can answer; the standard 2-second turn timeout still applies to every agent), and a mount for its reports.
3. Calls `/newgame?map=...&players=n` on it.
4. Starts one container per bot with the contract's env variables, in a random order (random seats; see "Seats").
5. Polls `/status` until the game is over (with an overall timeout).
6. Retrieves the final report (`/report`) and stores it.
7. Stops and removes all containers and the network, whatever happened.

Container limits, the same for every bot: `--read-only`, `--tmpfs /tmp`, non-root user, `--cpus <cores / n>` (see below), `--memory 256m`, `--pids-limit 64`, `--network <match net>`, no extra capabilities (`--cap-drop ALL`, `--security-opt no-new-privileges`). The limits are tuned after testing on real example bots. Docker's daemon is the only privileged component, and the ladder machine is a dedicated VM.

The target VM has only **2 virtual cores**, and with `--dev` matches are CPU-bound rather than paced by the clock. So **matches run one at a time**: nothing else competes for the CPU, and a bot's result does not depend on what else is running. Within a match, the CPU is split equally between the `n` bots: `--cpus = cores / n` (2 cores give 1.0 each in a 2-player match, 0.5 in a 4-player one, 0.33 in a 6-player one). The server container has no limit of its own: it only works between turns (validating and applying orders), when the bots are mostly waiting, so it should not take much from them. This is an assumption to verify: in step 2, measure match duration and the server's CPU use with the example bots, and adjust (e.g. reserve a fraction of a core for the server) if it turns out to matter. The per-bot CPU share differs between formats, but is identical for all bots in a match, which is what matters for fairness. If time per run turns out too tight, the answer is a larger VM or fewer matches per run, not unequal shares.

A bot is counted as **crashed** if its container exits before the game is over, and as **no-join** if it does not join in time (or its container exits first). The match goes on: the runner joins a stand-in in place of a missing bot, which posts empty orders every turn so the game is not slowed down. Either way the bot is ranked last (see "Scoring").

When a bot crashes mid-game, the runner forfeits its seat through the server's `/forfeit` route (admin token only): the server stops waiting for its orders, so the rest of the game is not slowed down by the turn timeout.

## Games and lineups

Maps are fixed by the files in `maps/` (the number of players per map is chosen by the schedule: any `n` the map supports). The mix of formats:

- mostly 3-4 player matches,
- 6-player matches as the showcase format,
- 2-player matches (a full head-to-head round-robin) for the head-to-head table.

With a few teams, a 6-player match is one big game with everyone in it: it says little about who beats whom, and one bot can decide the winner by interfering with another. Smaller lineups show direct strength.

## Scheduling: full round-robins

With a small pool (around 5-6 teams) there is no skill-based matchmaking. Everyone plays everyone, equally often.

**Per map with `n` players, a round-robin is every lineup of `n` bots:** C(B, n) lineups for B bots. With 6 bots that is 15 lineups on a 4-player map, 20 on a 3-player map, 15 pairs on a 2-player map, and a single lineup (everyone) on a 6-player map. Each bot meets each opponent equally often within a round-robin.

**Seats are random.** All maps are symmetrical, so a start position is neither an advantage nor a handicap: it only decides who your neighbours are. Seats are drawn at random for every match. Each lineup is played several times (best of N), so the luck of the neighbour draw averages out over the repeats.

**Runs.** The ladder runs twice a day at configured times (e.g. 8:00 and 16:00; staff can also trigger a run by hand). At the start of a run:

1. pull the registry and resolve every team's latest tag on `main` to a commit;
2. build the images that don't exist yet, and smoke-test the new ones (teams with a failing build keep their previous good image);
3. **freeze the pool**: the list of bots and commits for this run. Every match in the run uses exactly these images, so all results in a run are comparable and a team pushing a tag mid-run changes nothing until the next one.

A run then plays, for each configured map, one complete round-robin with every lineup played N times (best of N). The matches of a run are queued in an interleaved random order rather than lineup by lineup, so that if the run is cut short, every bot has still played about the same mix. A run has a deadline (before the next run starts); matches not started by then are dropped and the run is marked incomplete on the site. With only two runs a day, there are many hours available (about 8 between runs), so there is room for many matches. The run size is adjustable to the number of teams: the number of repeats N and the set of maps are configuration, and can be raised with few teams (more games, tighter standard errors) or lowered as the pool grows and lineups multiply. There is no auto-sizing: staff set N and the maps by hand, and the current values are published on the site, so players know how many games a run contains. Whatever N is chosen, it is the same for every lineup in a run. Between runs the machine is idle, which is a good time for builds and pruning.

**Staff bots.** There are no special house bots: the only way into the ladder is the registry. If wanted, staff register bots like any team (for instance the example agents, or a deliberately weak bot as a reference point), and they are ranked like everyone else. A format needs at least as many bots as players: with fewer registered bots, it is skipped in that run.

**Bot changes.** A team's new tag takes effect at the next run. Results are recorded with the commit that produced them.

**Pool changes.** A new team joins at the next run. A team whose bot repeatedly fails to build, join or finish (K matches in a row) is left out of the pool until it gets a good image; its old results age out of the window.

## Scoring

Rank players by flowers in reserve; equal counts are ties. Each player scores, per match:

```
points = 60 * (n - rank) / (n - 1)
```

Only 2, 3, 4 and 6 player games are played, so scores are scaled to integers: 60 is a multiple of every `n - 1` in use (1, 2, 3, 5), with a factor 2 to spare so that ties stay whole numbers too. Points per place, before ties:

| players | 1st | 2nd | 3rd | 4th | 5th | 6th |
|---|---|---|---|---|---|---|
| 2 | 60 | 0 | | | | |
| 3 | 60 | 30 | 0 | | | |
| 4 | 60 | 40 | 20 | 0 | | |
| 6 | 60 | 48 | 36 | 24 | 12 | 0 |

Tied bots share the average of the points of the places they span (two bots tied for 1st in a 4-player match get 50 each; all tied gives 30 each in a 3-player match). This puts maps with different player counts on one scale, and a bot's result is always an integer in 0-60, which is easy to read and to store. A match is never reduced to "winners vs. everyone else".

A bot that crashes or times out is ranked last, tied with other failed bots, so a broken bot loses points and cannot make others look good.

**Leaderboard score = mean points over a window of the last K runs** (K configurable; e.g. 2 runs for the last day, or more once there are enough games). Windows made of whole runs mean every bot in them has played the same lineups. Only recent runs count, so an updated bot is judged on its new behavior once the window has rolled over (and a team that pushed a new tag sees its effect after the next run). Each run's own standings are also shown, so a team can see directly what its latest tag did. Incomplete runs count with the matches they did play. There is no rating state to reset; the whole thing can be recomputed from the stored matches at any time. A fixed, all-vs-all format also means all teams play about the same number of games, so a win rate cannot be gamed by choosing when to play (the concern that made Elo unattractive).

The leaderboard shows, for each bot: score, games played in the window, a standard error, and the commit/tag currently running. With few teams, two bots are often statistically tied, and the site says so openly. Per-map scores are available as a secondary view.

A **head-to-head table** shows how often A finished ahead of B (ties count half), overall and per map. With 6 teams it is 15 cells, and it is what participants will want to look at.

If many more teams show up (when full round-robins become too large), the options are evenly drawn random lineups, or a rating system such as OpenSkill fed with the same stored results. The match records support either without changes.

## Recording

Every match is stored:

- **In SQLite**: match id, timestamp, map, `n`, seat/player index, team and commit per seat, flowers, rank, points, status per bot (finished, crashed, timeout), and whether the match is valid (e.g. server failure matches are marked void and not counted).
- **As files**: the server's JSON report (the same format as `history/` today, so the existing viewer reads it unchanged), stored compressed, with container logs of each bot (truncated to a size limit) for debugging. Without accounts, logs are public like the rest of the site; teams should not print anything secret.

The store is the source of truth: leaderboards, head-to-head tables and windows are queries over it.

**Durability.** Past data must never be lost:

- The data lives on the VM's disk, outside any container: match servers and bots are disposable, restarting them or Docker touches nothing.
- Each match is recorded as soon as it ends, in one transaction. Its files are written and synced to disk (written under a temporary name, synced, renamed) before the transaction that refers to them is committed. SQLite runs in WAL mode with full synchronization. A crash, a restart or a power loss loses at most the match being played.
- The ladder never deletes results. On restart, leftover containers are removed and an interrupted run is marked incomplete, keeping its matches.
- Low disk space is logged at start-up and before each run, and reported by `/health` (with Docker problems, failed runs, and a scheduler that stopped running), for the campus monitoring.
- Against the loss of the VM: a backup route protected by a URL token (`/backup?token=`) serves a tar archive named after the time, with a consistent database snapshot and all the files, to be fetched regularly by a backup system elsewhere.

## Public website

Read-only, served by the same binary. No accounts.

- **Leaderboard** with scores, games, standard errors, per-map views, the build status per team, and the head-to-head table.
- **Match list**, filterable by team and map, each entry linking to its replay and report.
- **Team page**: recent results, current commit/tag, build log and status.
- **Report download**: the game JSON (`/matches/<id>.json`, optionally gzipped), usable with the desktop viewer.
- **Replay in the browser**: the Ebitengine viewer compiled to WebAssembly, loading a match's report by URL (`viewer?match=<id>`), with the same controls as the desktop version. A proof of concept exists. The page for a match embeds it. Because of staff-run and campus-only access, no further access control is needed.
- A JSON API for the same data (`/api/leaderboard`, `/api/matches`, ...) is provided for display purposes only, so that for instance the big-screen view can be a simple page polling it.

Access is restricted to the campus network at the network level, as for the arena server.

## Live events and the big screen

The ladder runs twice a day during the competition week. For the end-of-week live event, the existing flow is kept: matches are played game by game on the regular server, with the teams' own bots, for show. The ladder helps with it: a **tournament mode** (`ladder tournament`) plays a one-off round-robin among chosen teams with its own formats, on the ladder machine, spawning matches the same way. It is recorded as a run of kind "tournament", shown with the runs on the website, and not counted in the leaderboard. It is a debugging and testing command: it must not run while the ladder service does (stop the service, or schedule it between runs). Seeding for the live event is taken from the ladder standings (top 8 if there are too many teams).

The **big-screen page** (`/screen`) shows the leaderboard next to the replays of the latest matches, autoplayed one after the other; a lunchtime tournament fills it with fresh matches. A true live view would need the ladder to relay the state of its private match servers to browsers; replays of matches that just ended are much simpler and look the same on a screen.

## Server changes

The server needs no change to work with the ladder, but a few small ones make the ladder and live events simpler and more robust. They are backward compatible (existing clients and agents are unaffected), and they come first in the implementation plan (step 0).

1. **Configurable directories.** `--maps` and `--history` flags (today the maps directory is a constant, and both are relative to the working directory). The runner mounts a fresh report directory per match.
2. **Report route.** `GET /report?id=<game id>` returns the JSON report of a finished game (the same format as the files in `history/`), so the runner does not have to find a file whose name embeds a timestamp. The `/history/` file route stays for browsing.
3. **Random seats.** A joining player gets a random free seat instead of the next one in join order, so seating does not depend on container start-up timing (ladder) or on who joins first (live events). The `players` list in the report is indexed by seat.
4. **Results in the report.** A `result` block in the report and in the finished game's status, with the final flowers, rank and winners per player. The ladder and website then do not re-derive scores from the last state.
5. **CORS and websocket origin.** CORS headers on the read routes and an origin check that accepts the configured origins, so the web viewer can talk to the arena server directly during live events. Not needed for the ladder itself, since the ladder site serves its own reports.
7. **Forfeit route** (added during implementation). `GET /forfeit?id=&token=<admin token>&player=` stops waiting for a player's orders. The runner uses it when a bot's container exits mid-game: otherwise every remaining turn waits for the 2-second timeout, and a crashing bot would make its matches last minutes.
6. **Lighter Docker image.** A multi-stage `Dockerfile` (build with the Go toolchain, run from a small image with the binary and `maps/`) and a `.dockerignore` excluding `history/`, so per-match server start-up is fast.

Not changed: `--dev` stays a process flag (the ladder spawns a server per match, with `--dev`; the 2-second turn timeout stays), and `/newgame` stays unauthenticated (the ladder's servers are on private networks, the live server is campus-only).

## Deployment

A campus VM, handed over with root. The ladder binary runs under systemd with access to the Docker daemon, with:

- Docker installed, the arena server image built from the repo (`runDocker.sh` / `Dockerfile`),
- a data directory (SQLite, reports, logs, cloned repos),
- the configuration file: registry repo URL, run times, maps and formats, N, window (K runs), resource limits (CPU cores to split), timeouts,
- the web server on one port, behind the campus network restriction.

If load grows, the one-VM design can be split later (runner vs web), as everything between them goes through the store.

## Implementation status

All the steps of the plan are implemented, on the `server-ladder-support` branch, one commit per step:

0. **Server changes**: items 1-4, 6 and 7 above. Item 5 (CORS) is not done: the ladder serves the viewer and reports itself, so it is only needed if the web viewer should talk to a live arena server.
1. **Bot contract and templates**: [docs/docker.md](docs/docker.md); Dockerfiles for both example agents, which read `ARENA_HOST`, `ARENA_GAME_ID` and `ARENA_NAME`.
2. **Match runner** (`ladder/runner.go`): with the example bots, a match takes 1-2 seconds; a full test run of 25 matches took under a minute. Real bots will be slower: measure with the first real teams and set `repeats` accordingly.
3. **Store and scoring** (`ladder/store.go`, `ladder/scoring.go`).
4. **Scheduler** (`ladder/scheduler.go`): `ladder` runs the ladder (scheduled runs, SIGUSR1 for a run now, and the website); the other commands are for debugging and testing.
5. **Registry and builder** (`ladder/registry.go`, `ladder/builder.go`). Failed builds are not retried automatically (a broken tag stays broken); `ladder rebuild <team>` forgets a failed build after a transient failure.
6. **Website** (`ladder/web.go`, `ladder/web/`).
7. **Web viewer**: `viewer/build-web.sh`, served at `/viewer/`.
8. **Tournament mode and big-screen page**: round-robin tournaments only (no elimination brackets, which matter little with a handful of teams).
9. **Deployment**: [ladder/deploy/setup.md](ladder/deploy/setup.md) and a systemd unit. To do on the VM: dry run with the example bots and a few volunteer bots, then open registration.

The game review happens after this work.

## Decisions

- **Game review.** A pass over rules and balance, so this year's game is similar in accessibility but not identical, is separate and comes after the ladder. Maps, viewer and example agents may change with it, so the ladder treats them as data (maps are read from `maps/`, reports are in the standard format).
- **Pacing.** Ladder servers run with `--dev`, with the standard turn timeout for all agents.
- **Tags.** The latest tag on the `main` branch of a team's repo.
- **Retention.** Reports, logs and results are kept forever (the store is small; compress reports, and truncate container logs per match, not per age).
- **Hosting.** A campus VM with 2 virtual cores; see Running a match: matches run one at a time.

## Open questions

- Is the VM's Docker access and the campus-only restriction confirmed, and where will the public site be reachable from (hostname)?
