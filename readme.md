# Hive Arena

A friendly AI competition for the Hive students!

![Screenshot](docs/arena-viewer.png)

## The game

A strategy game of bees, hives and flowers, on a hexagonal map. Each player starts with a hive and a few bees: bees gather flowers from fields and bring them back to their hives, which spawn more bees; bees can also build new hives and wax walls, and attack other bees to steal their flowers. Fields regrow unless they are emptied. All players give their orders at the same time, every turn, and see only what is near their units. After a fixed number of turns, the player with the most flowers wins.

Maps are randomly generated for every game, and exactly symmetric: every player starts with the same surroundings.

The full rules: [docs/rules.md](docs/rules.md).

## Agents

Players write **agents**: programs that join a game on the **arena server**, read the state of the game every turn, and send back their orders, within 2 seconds. Agents talk to the server over HTTP with JSON, and can follow the turns on a websocket: any language will do.

- **Templates** in [Go](example-agent-go) and [Lua](example-agent-lua) handle the communication, and leave you a function that receives the state of the game and returns the orders for the turn. They only move their bees at random: the rest is yours. See the readme in each directory.
- **Another language:** the API is described in [docs/API.md](docs/API.md). An agent joins a game (`/join`), then, every turn, gets the state (`/game`) and sends its orders (`/orders`); the websocket (`/ws`) says when each turn begins.
- **Running a server locally**, to test an agent: `go run ./server -dev`. Its options, maps, and how to deploy it: [docs/server.md](docs/server.md).
- **Watching games:** the [viewer](viewer) replays finished games, or follows games live.

## The ladder

During the competition, the **ladder** plays every registered team's agent against the others, all day, without anyone having to run matches: teams challenge the teams near them on the ladder, ratings move after every match, and every match can be replayed on its website. A team registers once, then submits a new version by pushing a tag: it plays within minutes.

- **Taking part, the website and its data** (results, replays, a script to download your matches): [docs/ladder.md](docs/ladder.md)
- **The bot contract:** the ladder builds your agent from a `Dockerfile` in your repository, and runs it with equal resources for everyone: [docs/docker.md](docs/docker.md). Both templates come with one.
- **For staff:** running and deploying the ladder: [ladder/readme.md](ladder/readme.md), [ladder/deploy/setup.md](ladder/deploy/setup.md)

## Repository

- `arena/`: the game itself (rules, maps), shared by everything else
- `server/`: the arena server
- `viewer/`: the viewer, for the desktop and the browser
- `example-agent-go/`, `example-agent-lua/`: the agent templates
- `ladder/`: the ladder service
- `docs/`: the documentation
- `scripts/`: small tools (games from the command line, downloading ladder matches)

## License

The Hive Arena source code is Copyright (c) Hive Helsinki 2025-2026, and released under the MIT License
