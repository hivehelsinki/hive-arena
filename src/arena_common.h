#ifndef ARENA_COMMON_H
#define ARENA_COMMON_H

#define NUM_ROWS 25
#define NUM_COLS 30

#define NUM_PLAYERS 2
#define NUM_BEES 5

#define TURNS_BEFORE_TIMEOUT 1000

#define VIEW_DISTANCE 3
#define VIEW_SIZE (VIEW_DISTANCE * 2 + 1)

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
    int row;
    int col;
} coords_t;

int is_bee(cell_t cell);
int is_hive(cell_t cell);
int cell_player(cell_t cell);
int is_bee_with_flower(cell_t cell);
cell_t bee_cell(int player, int with_flower);
cell_t hive_cell(int player);
coords_t direction_to_coords(coords_t from, dir_t direction);
void turn_to_player_and_bee(int turn, int *player, int *bee);

void panic(const char *message);

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

#endif // ARENA_COMMON_H
