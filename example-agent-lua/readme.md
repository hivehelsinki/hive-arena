# Example agent in Lua

## Setup

- Install Lua 5.4 and luarocks (previous versions not tested but might be fine)
- Run `luarocks install lunajson` and `luarocks install http`

## Quick start

Run `lua main.lua <host> <gameid> <name>` in the agent's directory to join the game `gameid` on the arena server running at `host`. `name` is a free string you can use to name your agent or team in the game logs.

For instance: `lua main.lua localhost:8000 bright-crimson-elephant-0 SuperTeam`

The library expects you to implement a callback function. It is called at each round of the game with the current game state (limited to what your agent can see) and your player ID. It should return an array of order tables that represent all the commands you want to give to your units.

The conversion to and from JSON is done as closely as possible: JSON arrays become tables indexed from 1, JSON dictionaries become Lua tables with string keys, constants remain as strings. Strings, booleans and numbers remain as is.

## Docker

When no command line arguments are given, the agent reads the `ARENA_HOST`, `ARENA_GAME_ID` and `ARENA_NAME` environment variables instead. That is how the ladder runs agents, from a Docker image: build this one with `docker build -t example-agent-lua .` in this directory. See [docs/docker.md](../docs/docker.md) for the conventions your own Dockerfile has to follow.

## Test script

A very basic script is provided to start a new game and run a number of agents against each other automatically. Some values are hardcoded, tweak at will.

Run `lua test.lua <host>`.
