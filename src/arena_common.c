#include <stdio.h>
#include <stdlib.h>
#include "arena_common.h"

int is_bee(cell_t cell)
{
    switch(cell)
    {
        case BEE_0:
        case BEE_1:
        case BEE_0_WITH_FLOWER:
        case BEE_1_WITH_FLOWER:
            return 1;
        default:
            return 0;
    }
}

int is_hive(cell_t cell)
{
    switch(cell)
    {
        case HIVE_0:
        case HIVE_1:
            return 1;
        default:
            return 0;
    }
}

int cell_player(cell_t cell)
{
    switch(cell)
    {
        case BEE_0:
        case BEE_0_WITH_FLOWER:
        case HIVE_0:
            return 0;
        case BEE_1:
        case BEE_1_WITH_FLOWER:
        case HIVE_1:
            return 1;
        default:
            return -1;
    }
}

int is_bee_with_flower(cell_t cell)
{
    switch(cell)
    {
        case BEE_0_WITH_FLOWER:
        case BEE_1_WITH_FLOWER:
            return 1;
        default:
            return 0;
    }
}

cell_t bee_cell(int player, int with_flower)
{
    if (player == 0 && with_flower == 0)
        return BEE_0;
    else if (player == 0 && with_flower == 1)
        return BEE_0_WITH_FLOWER;
    else if (player == 1 && with_flower == 0)
        return BEE_1;
    else // (player == 1 && with_flower == 1)
        return BEE_1_WITH_FLOWER;
}

cell_t hive_cell(int player)
{
    if (player == 0)
        return HIVE_0;
    else
        return HIVE_1;
}

static const coords_t offsets[] = {
    {-1, 0},
    {-1, 1},
    { 0, 1},
    { 1, 1},
    { 1, 0},
    { 1,-1},
    { 0,-1},
    {-1,-1}
};

coords_t direction_to_coords(coords_t from, dir_t direction)
{
    coords_t offset = offsets[direction];

    return (coords_t) {
        .row = from.row + offset.row,
        .col = from.col + offset.col
    };
}

void turn_to_player_and_bee(int turn, int *player, int *bee)
{
    *player = turn % NUM_PLAYERS;
    *bee = turn / 2 % NUM_BEES;
}

void panic(const char *message)
{
    printf("Fatal error: %s\n", message);
    exit(1);
}
