Protocol
========

The arena program and the two agents communicate via TCP connections, as described below.

Note: you do not need this document unless you plan on using another language than C. The `libagent` library abstracts all this away from you so you can focus on your AI.

Description
-----------

- The protocol is text based.
- Each message ends with the newline character `\n`.
- Lines must not exceed 200 characters, including the newline character.
- Once the connection to a player is established, any time the arena expects an answer, the player has 2 seconds to send it. In case of timeout or error while deserializing, the player will be considered forfeit, and all current connections will be closed.

To initiate connections:

- The arena program listens on the given port and accepts exactly 2 incoming connections.
- Each agent, immediately after connecting, must send a message containing its team name.

Once the two agents are connected, the game begins. At each turn of the game:

- The arena sends to the current player a serialized `agent_info_t`
- Then, waits for that player to send a serialized `command_t`

Once the game is over:

- The arena sends the message `gameover` to both players and shuts down the connections.

Serialization
-------------

The necessary data types are defined in C as follows, and their serialization matches closely their definition:

```
typedef enum
{
    EMPTY,
    BEE_0,
    BEE_1,
    BEE_0_WITH_FLOWER,
    BEE_1_WITH_FLOWER,
    FLOWER,
    WALL,
    HIVE_0,
    HIVE_1,
    OUTSIDE
} cell_t;

typedef enum
{
    N,
    NE,
    E,
    SE,
    S,
    SW,
    W,
    NW
} dir_t;

typedef enum
{
    MOVE,
    FORAGE,
    BUILD,
    GUARD
} action_t;

typedef struct
{
    int turn;
    int player;
    int bee;
    int row;
    int col;

    cell_t cells[VIEW_SIZE][VIEW_SIZE];
} agent_info_t;

typedef struct
{
    action_t action;
    dir_t direction;
} command_t;
```

Struct is serialized as follows:

- Fields are serialized in the order they are defined, separated by commas
- `int` or enums fields are serialized as their value in decimal
- The `cells` array is written as one single string of digits, row by row, where each cell becomes the single digit representing the value of the enum (notice there are exactly 10 different possible `cell_t` values)

### Examples

- A command to move northwest is serialized as `0,7`
- An `agent_info_t` sent at the start of the game might look like: `0,0,0,10,2,9000050900000090000009001000900100090710009001000` (turn 0, player 0, bee 0, row 10, column 2, and then `VIEW_SIZE` * `VIEW_SIZE` characters representing the cells within view distance of the active bee, written row by row)
- Note in particular that cells in the viewpoint of the bee that fall outside the bounds of the arena take the special value `OUTSIDE` (9).
