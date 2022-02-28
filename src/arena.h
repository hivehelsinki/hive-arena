#ifndef ARENA_H
#define ARENA_H

#include "arena_common.h"

#define NUM_FLOWERS_MIN 30
#define NUM_FLOWERS_RANGE 10

typedef struct
{
    unsigned int random_seed;

    cell_t cells[NUM_ROWS][NUM_COLS];
    coords_t bees[NUM_PLAYERS][NUM_BEES];

    int turn;
    int scores[NUM_PLAYERS];
    int last_point_turn;

    int game_over;
    int winner;
} arena_t;

arena_t *arena_new(int seed);
void arena_delete(arena_t *arena);
cell_t arena_get(const arena_t *arena, coords_t coords);
void arena_set(arena_t *arena, coords_t coords, cell_t type);
int arena_apply_command(arena_t *arena, command_t command);
int arena_random(arena_t *arena, int max);
agent_info_t get_bee_point_of_view(arena_t *arena);

#endif // ARENA_H
