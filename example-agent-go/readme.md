# Example agent in Go

## Quick start

Run `go run . <host> <gameid> <name>` in the agent's directory to join the game `gameid` on the arena server running at `host`. `name` is a free string you can use to name your agent or team in the game logs.

For instance: `go run . localhost:8000 bright-crimson-elephant-0 SuperTeam`

The library expects you to implement a `think` function with the following prototype: `func think(state *arena.GameState, player int) []arena.Order`. It is called at each round of the game with the current game state (limited to what your agent can see) and your player ID. It should return a slice of Order structs that represent all the commands you want to give to your units.

All types are defined in the `arena` package (the `arena` directory of this repository, imported as `github.com/hivehelsinki/hive-arena/arena`), and mirror closely the structures expected and returned by the API.

## Docker

When no command line arguments are given, the agent reads the `ARENA_HOST`, `ARENA_GAME_ID` and `ARENA_NAME` environment variables instead. That is how the ladder runs agents, from a Docker image: build this one with `docker build -f example-agent-go/Dockerfile -t example-agent-go .` from the repository root. See [docs/docker.md](../docs/docker.md) for the conventions your own Dockerfile has to follow.

## Test script

A very basic script is provided to start a new game and run a number of agents against each other automatically. Some values are hardcoded, tweak at will.

Run `lua test.lua <host>`.
