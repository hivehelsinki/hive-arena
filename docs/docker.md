# Running your agent in Docker (ladder bot contract)

The ladder builds and runs every team's agent with Docker. Your repository must provide a `Dockerfile` that follows the conventions below. The organizers only build and run it: if it works on your machine with the commands at the end of this page, it works on the ladder.

## What the ladder does

- Registers your repository through the public registry (see the ladder page). The repository must be public, with an `https://` URL.
- Takes the most recent tag on the `main` branch of your repository, and builds it with `docker build` from the repository root, using the `Dockerfile` at the root (another context and Dockerfile can be registered). New tags are built within about 15 minutes, and play from the next match: the result and the build log are on your team's page.
- Before a new version plays, it must pass a smoke test: a 1-player game on `tiny`, played until the end.
- For each match, starts a fresh game server and one container per agent, on a private network.

## The contract

- **One container plays one game, then exits.** The image is started with no command line arguments: it must use its default `ENTRYPOINT`/`CMD`.
- **Configuration comes from environment variables:**
  - `ARENA_HOST`: host and port of the game server, e.g. `server:8080`
  - `ARENA_GAME_ID`: the ID of the game to join, private to your agent: use it for every call (`/join`, `/game`, `/orders`, `/ws`)
  - `ARENA_NAME`: the name to join with (your team's name: with your private game ID, you always play under it)
- **Join, play until the game is over, exit.** Exit with status 0 once the game is over. A container that exits before the end of the game, or never joins, counts as a crash: it ranks last in that match.
- **No network access** other than the game server. Everything your agent needs must be in the image: fetch dependencies during the build, not when the agent starts.
- **Read-only filesystem**, except `/tmp` (which counts in the memory limit). The agent runs as an unprivileged user (uid 65534, whatever `USER` the image sets): files it needs must be readable by everyone.
- **Equal resources for everyone:** the CPU is split equally between the agents of a match (one match runs at a time), and each agent has 256 MB of memory and a limited number of processes. The standard turn timeout (2 seconds) applies.
- **Output:** what your agent prints is kept (the last 256 kB per match) and shown on the match page, which helps you debug it. Docker keeps at most 1 MB per container: don't print every turn's whole state.
- **Building** happens with network access, under a time limit (10 minutes), and the image must be under 2 GB. Keep images small: use a multi-stage build when your language allows it. Build cache mounts (`RUN --mount=type=cache`) are emptied before each build.

## Examples

Both example agents read the environment variables when they are not given command line arguments, and come with a Dockerfile:

- [example-agent-go/Dockerfile](../example-agent-go/Dockerfile): compiles the agent and copies the binary into a minimal image. In this repository it is built from the repository root (`docker build -f example-agent-go/Dockerfile .`) because it uses the `arena` package from the same module; in your own repository with your own `go.mod`, put the Dockerfile at the root and adjust the paths.
- [example-agent-lua/Dockerfile](../example-agent-lua/Dockerfile): installs Lua and the needed rocks, then copies the scripts.

For other languages, the pattern is the same: install the dependencies and build at image build time, and set the `ENTRYPOINT` to start your agent.

## Testing locally

Run a server, create a game, and start your agent the way the ladder does:

```sh
docker build -t my-agent .
docker network create arenatest

# The arena server, from a clone of the arena repository
docker build -t arena-server path/to/hive-arena
docker run -d --rm --name server --network arenatest -p 8000:8080 arena-server

# Create a 1-player game and run your agent in it, with the ladder's restrictions
ID=$(curl -s "localhost:8000/newgame?map=tiny&players=1" | sed 's/.*"id":"\([^"]*\)".*/\1/')
docker run --rm --network arenatest --read-only --tmpfs /tmp --cap-drop ALL \
  --user 65534:65534 --security-opt no-new-privileges --cpus 1 --memory 256m --pids-limit 64 \
  -e ARENA_HOST=server:8080 -e ARENA_GAME_ID=$ID -e ARENA_NAME=me my-agent

# Then look at the result
curl -s "localhost:8000/report?id=$ID"
```

The ladder's network is also internal (no access to the internet); add `--internal` to `docker network create` to check that your agent does not need it.
