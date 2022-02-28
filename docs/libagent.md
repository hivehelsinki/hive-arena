libagent
========

The arena comes with a C library you can use to write your agents. It handles the connection to the arena and the network protocol, so that you can focus on writing the logic for your agent.

How to use
----------

The include files are in the `src` directory. You should include `agent.h` in your source, which itself includes `arena_common.h` for common data types and utilities.

Your program should link with the static library `bin/libagent.a`.

The main (and only) libagent function is declared as follows:

`int agent_main(char *host, int port, char *team_name, command_t (*think_function)(agent_info_t));`

- `host` is a string containing the hostname or IP address of the machine running the arena program
- `port` is the port on which is listening the arena program
- `team_name` is a string containing the name of your team. It will be saved with the logs and displayed on the replays.
- `think_function` is the function describing the logic of your AI

Once called, `agent_main` will loop until the end of the game, after which it will return. All you have to do is implement the `think_function`.

`think_function`
----------------

Whenever it is your turn to play, the `think_function` will be called with an `agent_info_t` struct as argument, which will give you information about the current active bee (most importantly its surroundings). You should then return a `command_t` struct, which indicates what you want the bee to do.

`agent_info_t` is defined as follows:

```
typedef struct
{
    int turn;
    int player;
    int bee;
    int row;
    int col;

    cell_t cells[VIEW_SIZE][VIEW_SIZE];

} agent_info_t;
```

- `turn` is the current turn, which starts at 0.
- `player` is the current active player. In the point of view of your agent, it will always contain the same number (0 or 1).
- `bee` is the index of your current active bee, from 0 to 4.
- `row` and `col` are the coordinates of your active bee.
- `cells` is a 2D array of the cells that your active bee can see, indexed as `cells[row][column]`.

You'll notice that `VIEW_SIZE` is defined as `#define VIEW_SIZE (VIEW_DISTANCE * 2 + 1)`. The `cells` array is centered on your bee, which therefore is at index `[VIEW_DISTANCE][VIEW_DISTANCE]`.

After having processed the information and decided on a course of action, your `think_function` should return a `command_t` struct, defined as follows:

```
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
    action_t action;
    dir_t direction;
} command_t;
```

See `arena_common.h` for all other definitions, including useful functions to analyze cells.

Example
-------

The `example-agent` directory shows a basic example of how to use the libagent library. It implements the following logic:

- if the bee doesn't yet have a flower and is next to one, pick it up
- if the bee has a flower and is next to its team's hive, drop the flower in it
- otherwise, walk in a random direction

It probably is the bare minimum an agent can do to mark points, and surprisingly, it is enough to do so! That can provide you a baseline on which to improve :)
