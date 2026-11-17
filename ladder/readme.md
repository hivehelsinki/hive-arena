# Hive Arena ladder

The ladder service: plays registered bots against each other in Docker containers, records the results, and publishes them. See [LADDER.md](../LADDER.md) for the design.

Work in progress. Currently available:

## Playing one match

```sh
# Images of the server and of the bots
docker build -t hive-arena-server .
docker build -t example-agent-go -f example-agent-go/Dockerfile .
docker build -t example-agent-lua example-agent-lua

go run ./ladder match [-report report.json] [-logs dir] balanced a=example-agent-go b=example-agent-lua
```

Each bot is given as `name=image`. The runner creates a private network, starts a server and the bots (with the resource limits of the ladder), waits for the end of the game, and prints the outcome of each bot:

- `finished`: played until the end
- `crashed`: joined, but exited before the end of the game
- `no-join`: did not join in time (or could not be started); a stand-in played its seat, posting no orders

Containers and networks are always removed at the end. All of them have the `hive-ladder` label.

## Tests

`go test ./ladder` runs the runner tests with Docker (skipped with `-short` or when Docker is unavailable). One of them plays a game with a crashing bot, which takes about 2 minutes, since every remaining turn waits for the turn timeout.
