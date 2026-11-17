# Hive Arena 2025

A friendly AI competition for the Hive students!

![Screenshot](docs/arena-viewer.png)

## Running the arena

- Install the Go compiler (for instance `brew install go` on MacOS, or `apt install golang-go` or `snap install go` on Ubuntu, or follow instructions from the official website).
- Run `go run ./server`. Optionally add the `-p <port>` option to select which port should the server listen on, `--maps <dir>` to load maps from another directory than `maps`, and `--history <dir>` to save game reports in another directory than `history`.

The server is now ready to host games. Multiple games can run concurrently.

In addition to the API routes to be used programmatically, the `/status` route shows information about all currently running games, `/history` contains JSON reports of past completed games, and `/report?id=<game id>` returns the report of a given finished game.

## Development mode

By default, the server ensures a minimum turn duration of 0.5 seconds. To bypass that restriction, for instance for local automated testing, you can pass the `--dev` command line option to the server.

## Building for production

Building an executable with `go build -C server .` will embed the git revision and the executable will print it at startup. By default, the server looks for the `maps` directory in the current working directory (see the `--maps` option), so it can be run from the repo root as `./server/server -p port`.

A Dockerfile is also provided for smoother deployment. Follow the usual Docker building process, or use the `runDocker.sh` script.

## Using the provided agent templates

Example agents are provided in Lua and Go. These templates abstract the network communication and let you implement a simple callback that receives the current game state, and expects a list of commands to play for the turn.

That allows you to focus on the logic of your agent instead of the implementation details.

See readme files in each relevant directory.

To take part in the ladder, an agent must come with a Dockerfile following the conventions in [docs/docker.md](docs/docker.md). Both templates provide one.

## Developing agents in other languages

To start an agent from scratch, you will need to implement the network protocol yourself. An agent should:

- be able to open HTTP connections (both GET and POST methods)
- read or convert JSON payloads
- optionally open a websocket connection

See the [API definition](docs/API.md) for all the necessary routes. An agent has to:

- join a game on the arena server (`/joingame` route)
- once per turn: poll the current game state (`/game` route), and send back orders for the units (`/orders` route) within 2 seconds of the turn's start
- optionally, to avoid polling the state too often, or missing a turn, the agent can also listen to the game's websocket (`/ws` route), which informs in realtime when a new turn begins

## License

The Hive Arena source code is Copyright (c) Hive Helsinki 2025, and released under the MIT License
