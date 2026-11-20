# Arena API

The Arena server uses HTTP as a communication protocol. All routes can expect parameters as a query string and/or a JSON payload, and respond with a JSON reply.

Requests return standard HTTP status codes such as OK (200), Bad Request (400), Forbidden (403), Internal Server Error (500).

## GET /newgame

Query string parameters:

- `map`: the name of the map to load: `random` for a new random map (for 2, 3, 4 or 6 players, exactly symmetric), `random:<seed>` for a given random map, or the name of a file of the maps folder in the Arena repository.
- `players`: the number of players to spawn on the map. Between 1 and 6.
- `names` (optional): comma-separated names of the players, one per player, all different. The seats are then reserved (see below).

This creates a new game on the server, with a randomly generated ID such as `blithe-lavender-tapir-4`. The game is then expecting players to join.

**Reserved seats**: with `names`, the response also lists one private game ID per name (`joinIds`). Each player must be given its own private ID: it joins under its reserved name (whatever `name` it sends), only once, and can use that ID instead of the public one for all the other routes. The public ID then cannot be used to join. This lets organizers (such as the ladder) make sure that each agent plays under its own name, and that no one takes someone else's seat.

A server started with `-max-games N` creates at most N games, then answers Forbidden.

Response:

```
{
	"id": (string) the game ID,
	"numPlayer": (int) the number of players the games expects (equal to the 'players' parameter),
	"map": (string) the chosen map (equal to the 'map' parameter; a new random map is given as "random:<seed>"),
	"createdDate": (string) the time of creation of the game, in ISO 8601 format,
	"adminToken": (string) an access token used to see the full state of the game (see '/game' route),
	"joinIds": (dictionary, name to string) the private game ID of each reserved seat, only with 'names'
}
```

## GET /status

Returns information about the server and all the games currently running.

```
{
	"gitRevision": (string) the git revision this executable was built from,
	"games": (array of statuses)
}
```

Each game status follows the following format:

```
{
	"id": (string) the game ID,
	"numPlayer": (int) the number of players the games expects,
	"players": (array of string) the names of the players who joined the game,
	"map": (string) the chosen map,
	"createdDate": (string) the time of creation of the game, in ISO 8601 format,
	"gameOver": (bool) whether the game is over or not,
	"result": (Result object) the final result, only once the game is over (see '/report')
}
```

## GET /join

Allows an agent to join a game that was created, but has not yet started.

Query string parameters:

- `id`: the ID of the game to join (or the private ID of a reserved seat)
- `name`: the name of the agent or team to announce to the server (ignored for reserved seats)

If the game is full, the response is the JSON string `"Game is full"`, and error code Bad Request. The same goes for a game with reserved seats joined with its public ID, or a reserved seat already taken. Otherwise:

```
{
	"id": (int) the ID of the player, from 0 to the number of players on the map - 1. Seats are assigned at random among the free ones, so the ID does not depend on the order in which players join.
	"token": (string) a unique personal token for that player, used for authentication in the other routes
}
```

When the game is full (all players have joined), it begins automatically.

## GET /game

Gets the current game state. If using the admin token, the full game state is returned. If using a player token, only the player's view is returned.

Query string parameters:

- `id`: the ID of the game to query
- `token`: the access token for the user

The game state is given in the following format:

```
{
	"numPlayers": (int) the number of players in the game,
	"turn": (int) the current turn (the first turn is 0),
	"rules": (Rules) the values of the game, see below,
	"hexes": (dictionary of Hex, with coordinates strings as keys) the current map of the game, including static and dynamic elements,
	"playerResources": (array of int) the number of flowers for each player,
	"gameOver": (bool) whether the game is over or not,
	"winners": (array of int) all the players who are tied for the win, if the game is over (can be a single value)
}
```

The rules are:

```
{
	"fieldFlowers": (int) the flowers in a field at the start, and at most,
	"regrowInterval": (int) a field with flowers left regains one every this many turns (an emptied field stays empty),
	"beeCost": (int), "hiveCost": (int), "wallCost": (int) the costs, in flowers,
	"wallAttackChance": (float) the chance that an attack destroys a wall,
	"stunChance": (float) the chance that an attack stuns a bee (and takes its flower),
	"fieldOfView": (int) how far players see from their hives and bees,
	"maxTurns": (int) the game ends after this many turns
}
```

Coordinates are encoded as `row,column` strings, where `row` and `column` are ints. Note that the server uses the "doubled width" coordinates system for "pointy tops" hexagons, as described here: https://www.redblobgames.com/grids/hexagons/.

Hexes are encoded as follows:

```
{
	"terrain": (string) one of "EMPTY", "ROCK", "FIELD",
	"resources": (int) the number of flowers in the hex, if any (and only if it is a field),
	"entity": (an Entity object) the entity currently present in the hex, if any
}
```

Entities are encoded as follows:

```
{
	"type": (string) one of "WALL", "HIVE", "BEE",
	"player": (int) the ID of the player owning this entity,
	"hasFlower": (bool) whether the entity is currently carrying a flower (only for bees)
}
```

When using a player token for this route, the `hexes` dictionary contains only hexes visible by bees and hives of the current player, and the `playerResources` array contains the value 0 for any other than the given player.

## POST /orders

Sets the commands for the entities of a player in the current turn.

Query string parameters:

- `id`: the ID of the game to send commands for
- `token`: the access token for the user
- `turn`: the turn the commands are meant for, which must be the current turn (the `turn` value of the state the commands were computed from)

Expected payload: an array of objects. Each command is in the following format:

```
{
	"type": (string) one of "MOVE", "ATTACK", "BUILD_WALL", "BUILD_HIVE", "FORAGE", "SPAWN",
	"coords": (coordinates string) the location of the entity this order applies to,
	"direction": (string) one of "E", "NE", "NW", "W", "SW", "SE"
}
```

The relative order of the commands in the array is significant (see [rules](rules.md)). The `direction` value is used only for certain orders and can be omitted for the others (see [rules](rules.md)).

Commands with an unknown type, or a missing or unknown direction when the type needs one, fail with the status `INVALID_ORDER`, and do not count as the unit's action for the turn.

If the token is correct, the JSON is valid, and the turn is the current one, the HTTP status code is always OK. This does not relate to whether the commands were successfully applied.

If the turn is not the current one (for example, if the commands were sent after the turn timeout), the commands are rejected with error code Bad Request.

The payload is limited to 1 MB, and to 2 × (the player's bees and hives) + 16 commands per turn: more could only fail, as each unit acts at most once per turn. Larger payloads are rejected with error code Bad Request.

The turn is processed once commands from all players are received, or after a fixed timeout (2 seconds).

A server can also limit the total time each player takes to answer over a game (`-time-budget`, see [server.md](server.md); the ladder uses 5 minutes). The time of a turn is the time from its start to the player's commands, or the whole timeout if they do not come. A player who exceeds the budget is forfeited (see `/forfeit`), its commands are rejected with error code Bad Request, and its result has `timedOut` set.

## GET /ws

A websocket specific to each game, that clients can listen to in order to avoid polling the game state too often.

Query string parameters:

- `id`: the ID of the game to get a websocket for

When the game begins, and every time a turn is processed, the following message is broadcasted to all listeners:

```
{
	"turn": (int) the turn that just begun,
	"gameOver": (bool) whether the last turn resulted in an end of game state
}
```

If a websocket is opened after the game has already begun, an initial message similar to the one above is sent to the listener to indicate the current turn.

After sending a message with `gameOver` set to `true`, the server closes the websocket.

## GET /forfeit

Stops waiting for a player's orders for the rest of the game, for instance because its agent crashed: turns are then processed as soon as the other players have posted their orders. The player stays in the game, its units simply get no orders. Meant for organizers and automated runners: it requires the admin token of the game.

Query string parameters:

- `id`: the ID of the game
- `token`: the admin token of the game (returned by `/newgame`)
- `player`: the ID of the player

The game must have started and not be over.

## GET /report

Returns the full report of a finished game. This is the same content as the files saved in the server's history directory, which the viewer can read.

Query string parameters:

- `id`: the ID of the game

If the game is still running, the response is an error with code Bad Request. If no game with this ID is known, the response is an error with code Not Found.

```
{
	"id": (string) the game ID,
	"map": (string) the map name,
	"createdDate": (string) the time of creation of the game, in ISO 8601 format,
	"players": (array of string) the names of the players, indexed by player ID,
	"history": (array of turns) the full state and the orders of every turn,
	"result": (Result object) the final result of the game
}
```

The result is given in the following format:

```
{
	"turns": (int) the number of turns played,
	"winners": (array of int) all the players who are tied for the win,
	"players": (array of objects, indexed by player ID) for each player:
		{
			"name": (string) the name of the player,
			"flowers": (int) the final number of flowers in reserve,
			"rank": (int) 1 + the number of players with strictly more flowers (tied players share the same rank),
			"forfeited": (bool, omitted when false) whether the player was forfeited (see '/forfeit')
		}
}
```

Reports of games played before this route existed do not include `result`.
