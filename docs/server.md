# The arena server

The arena server hosts games: agents join a game, read its state every turn and send their orders, through an HTTP and websocket API ([API.md](API.md)). The rules of the game are in [rules.md](rules.md).

## Running it

Install the Go compiler (for instance `brew install go` on macOS, `apt install golang-go` or `snap install go` on Ubuntu, or see the official website), then from the repository:

```sh
go run ./server
```

Options:

- `-p <port>`: the port to listen on (8000 by default)
- `-dev`: development mode, see below
- `-history <dir>`: where the reports of finished games are saved (`history` by default; empty: not saved)
- `-maps <dir>`: a directory of map files to load, see below
- `-time-budget <duration>`: the total time each player may take to answer over a game, e.g. `5m`; a player who exceeds it is forfeited (0, the default: no limit; the ladder sets it for its matches)
- `-max-games <n>`: create at most this many games, then refuse new ones (0, the default: no limit)

The server hosts any number of games at the same time. Besides the API routes, `/status` shows the games currently running, `/history` the reports of the finished ones, and `/report?id=<game id>` returns the report of a finished game.

## Development mode

By default, a turn lasts at least 0.5 seconds, so that games can be followed live. With `-dev`, a turn ends as soon as every player has sent its orders, for fast local tests. In both modes, a player has 2 seconds to send its orders.

## Maps

Games are played on random maps: `/newgame?map=random&players=N`, for 1, 2, 3, 4 or 6 players. They are exactly symmetric, so that every player starts with the same surroundings. A game's map is recorded as `random:<seed>`, and `map=random:<seed>` plays the same map again.

To train an agent on a specific scenario, write a map file in a directory given with `-maps`, and use its name without `.txt` as the map. The format is described with `arena.ParseMap` (in `arena/terrain.go`): one line per row of hexes, each hex two characters apart, odd rows shifted by one; `.` empty, `R` rock, `F` field, `H<player>` a hive, `B<player>` a bee.

## Watching games

The viewer shows a finished game from its report, or follows a game live: see [viewer/readme.md](../viewer/readme.md).

## Building for production

`go build -C server .` builds an executable that embeds the git revision, and prints it at startup. It can run from anywhere: `./server/server -p <port>`.

The `Dockerfile` at the root of the repository builds an image of the server (the ladder uses it for every match), and `runDocker.sh` builds and runs it.
