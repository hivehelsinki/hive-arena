#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "arena.h"

int arena_random(arena_t *arena, int max)
{
    return rand_r(&arena->random_seed) % max;
}

static void place_teams(arena_t *arena)
{
    int middle_row = NUM_ROWS / 2;

    arena->cells[middle_row][1] = HIVE_0;
    arena->cells[middle_row][NUM_COLS - 2] = HIVE_1;

    int bee_count = 0;
    const int team_0_col = 2;
    const int team_1_col = NUM_COLS - 3;

    for(int row = middle_row - NUM_BEES / 2 ; row <= middle_row + NUM_BEES / 2 ; row++)
    {
        arena->cells[row][team_0_col] = BEE_0;
        arena->cells[row][team_1_col] = BEE_1;

        arena->bees[0][bee_count] = (coords_t) {.row = row, .col = team_0_col};
        arena->bees[1][bee_count] = (coords_t) {.row = row, .col = team_1_col};

        bee_count++;
    }
}

static void place_flowers(arena_t *arena)
{
    int num_flowers = NUM_FLOWERS_MIN + arena_random(arena, NUM_FLOWERS_RANGE);
    int count = 0;

    while (count < num_flowers)
    {
        int row = arena_random(arena, NUM_ROWS);
        int col = arena_random(arena, NUM_COLS);

        cell_t *cell = &arena->cells[row][col];
        if (*cell == EMPTY)
        {
            *cell = FLOWER;
            count++;
        }
    }
}

arena_t *arena_new(int seed)
{
    arena_t *arena = (arena_t *)malloc(sizeof(arena_t));
    if (!arena)
        panic("Could not allocate memory for arena");

    arena->turn = 0;
    arena->random_seed = seed;

    arena->scores[0] = 0;
    arena->scores[1] = 0;
    arena->last_point_turn = -1;
    arena->game_over = 0;
    arena->winner = -1;

    for(int row = 0 ; row < NUM_ROWS ; row++)
    {
        for(int col = 0 ; col < NUM_COLS ; col++)
        {
            arena->cells[row][col] = EMPTY;
        }
    }

    place_teams(arena);
    place_flowers(arena);

    return arena;
}

void arena_delete(arena_t *arena)
{
    free(arena);
}

cell_t arena_get(const arena_t *arena, coords_t coords)
{
    if (coords.row < 0 || coords.row >= NUM_ROWS ||
        coords.col < 0 || coords.col >= NUM_COLS)
        return OUTSIDE;

    return arena->cells[coords.row][coords.col];
}

void arena_set(arena_t *arena, coords_t coords, cell_t type)
{
    if (coords.row < 0 || coords.row >= NUM_ROWS ||
        coords.col < 0 || coords.col >= NUM_COLS)
        panic("Arena write out of bounds");

    arena->cells[coords.row][coords.col] = type;
}

agent_info_t get_bee_point_of_view(arena_t *arena)
{
    int player, bee;

    turn_to_player_and_bee(arena->turn, &player, &bee);
    coords_t pos = arena->bees[player][bee];

    agent_info_t info = {
        .turn = arena->turn,
        .player = player,
        .bee = bee,
        .row = pos.row,
        .col = pos.col,
    };

    int start_row = pos.row - VIEW_DISTANCE;
    int start_col = pos.col - VIEW_DISTANCE;
    int end_row = pos.row + VIEW_DISTANCE;
    int end_col = pos.col + VIEW_DISTANCE;

    for (int row = start_row ; row <= end_row ; row++)
    {
        for (int col = start_col ; col <= end_col ; col++)
        {
            info.cells[row - start_row][col - start_col] = arena_get(arena, (coords_t){row, col});
        }
    }

    return info;
}
