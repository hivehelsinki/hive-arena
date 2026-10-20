# Arena API

The Arena server uses HTTP as a communication protocol. All routes can expect parameters as a query string and/or a JSON payload, and respond with a JSON reply.

Requests return standard HTTP status codes such as OK (200), Bad Request (400), Forbidden (403), Internal Server Error (500).

## GET /newgame

Query string parameters:

- `map`: the name of the map to load. See the maps folder in the Arena repository to see the available maps.
- `players`: the number of players to spawn on the map. Between 1 and 6.

This creates a new game on the server, with a randomly generated ID such as `blithe-lavender-tapir-4`. The game is then expecting players to join.

Response:

```
{
	"id": (string) the game ID,
	"numPlayer": (int) the number of players the games expects (equal to the 'players' parameter),
	"map": (string) the chosen map (equal to the 'map' parameter),
	"createdDate": (string) the time of creation of the game, in ISO 8601 format,
	"adminToken": (string) an access token used to see the full state of the game (see '/game' route)
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
	"gameOver": (bool) whether the game is over or not
}
```

## GET /join

Allows an agent to join a game that was created, but has not yet started.

Query string parameters:

- `id`: the ID of the game to join
- `name`: the name of the agent or team to announce to the server

If the game is full, the response is the JSON string `"Game is full"`, and error code Bad Request. Otherwise:

```
{
	"id": (int) the ID of the player, from 0 to the number of players on the map - 1,
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
	"hexes": (dictionary of Hex, with coordinates strings as keys) the current map of the game, including static and dynamic elements,
	"playerResources": (array of int) the number of flowers for each player,
	"lastResourceChange": (int) the last turn during which a flower was dropped in a hive,
	"gameOver": (bool) whether the game is over or not,
	"winners": (array of int) all the players who are tied for the win, if the game is over (can be a single value)
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

If the token is correct, the JSON is valid, and the turn is the current one, the HTTP status code is always OK. This does not relate to whether the commands were successfully applied.

If the turn is not the current one (for example, if the commands were sent after the turn timeout), the commands are rejected with error code Bad Request.

The turn is processed once commands from all players are received, or after a fixed timeout (2 seconds).

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
