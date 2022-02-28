#include "arena.h"

typedef struct
{
    int player;
    int bee;
    coords_t position;
    cell_t bee_type;
    coords_t target;
    cell_t target_type;
} turn_info_t;

static int empty_neighbours(arena_t *arena, coords_t cell, coords_t *result)
{
    int count = 0;

    for(int i = 0 ; i < 8 ; i++)
    {
        coords_t neighbour = direction_to_coords(cell, i);
        cell_t type = arena_get(arena, neighbour);

        if (type == EMPTY)
        {
            result[count] = neighbour;
            count++;
        }
    }

    return count;
}

static turn_info_t get_turn_info(arena_t *arena, dir_t direction)
{
    turn_info_t info;

    turn_to_player_and_bee(arena->turn, &info.player, &info.bee);
    info.position = arena->bees[info.player][info.bee];
    info.target = direction_to_coords(info.position, direction);

    info.target_type = arena_get(arena, info.target);
    info.bee_type = arena_get(arena, info.position);

    return info;
}

static int apply_move(arena_t *arena, dir_t direction)
{
    turn_info_t info = get_turn_info(arena, direction);

    if (info.target_type != EMPTY)
        return 0;

    if (is_bee_with_flower(info.bee_type) && arena_random(arena, 3) == 0)
        return 0;

    arena_set(arena, info.target, info.bee_type);
    arena_set(arena, info.position, EMPTY);

    arena->bees[info.player][info.bee] = info.target;

    return 1;
}

static int apply_forage(arena_t *arena, dir_t direction)
{
    turn_info_t info = get_turn_info(arena, direction);

    int carrying = is_bee_with_flower(info.bee_type);

    if (!carrying && info.target_type == FLOWER)
    {
        arena_set(arena, info.target, EMPTY);
        arena_set(arena, info.position, bee_cell(info.player, 1));

        return 1;
    }

    if (carrying && info.target_type == EMPTY)
    {
        arena_set(arena, info.target, FLOWER);
        arena_set(arena, info.position, bee_cell(info.player, 0));

        return 1;
    }

    if (carrying && is_hive(info.target_type))
    {
        int owner = cell_player(info.target_type);
        arena->scores[owner]++;
        arena->last_point_turn = arena->turn;
        arena_set(arena, info.position, bee_cell(info.player, 0));

        return 1;
    }

    return 0;
}

static int apply_build(arena_t *arena, dir_t direction)
{
    turn_info_t info = get_turn_info(arena, direction);

    if (info.target_type == EMPTY)
    {
        arena_set(arena, info.target, WALL);

        return 1;
    }

    return 0;
}

static int apply_guard(arena_t *arena, dir_t direction)
{
    turn_info_t info = get_turn_info(arena, direction);

    if (info.target_type == WALL || info.target_type == FLOWER)
    {
        if (arena_random(arena, 2) == 0)
        {
            arena_set(arena, info.target, EMPTY);
            return 1;
        }
    }
    else if (is_bee_with_flower(info.target_type))
    {
        coords_t empties[8];
        int count = empty_neighbours(arena, info.target, empties);

        if (count > 0 && arena_random(arena, 2) == 0)
        {
            coords_t drop_location = empties[arena_random(arena, count)];

            arena_set(arena, drop_location, FLOWER);
            arena_set(arena, info.target, bee_cell(cell_player(info.target_type), 0));

            return 1;
        }
    }

    return 0;
}

static int (*action_functions[4])(arena_t *, dir_t) = {
    apply_move,
    apply_forage,
    apply_build,
    apply_guard
};

static int arena_count_flowers(const arena_t *arena)
{
    int count = 0;
    for(int row = 0 ; row < NUM_ROWS ; row++)
    {
        for(int col = 0 ; col < NUM_COLS ; col++)
        {
            cell_t cell = arena->cells[row][col];
            if (cell == FLOWER || is_bee_with_flower(cell))
                count++;
        }
    }

    return count;
}

static void check_endgame(arena_t *arena)
{
    int timeout =
        arena->turn >= arena->last_point_turn + TURNS_BEFORE_TIMEOUT &&
        (arena->turn + 1) % (NUM_BEES * NUM_PLAYERS) == 0;

    if (timeout || arena_count_flowers(arena) == 0)
    {
        arena->game_over = 1;

        if (arena->scores[0] > arena->scores[1])
            arena->winner = 0;
        else if (arena->scores[0] < arena->scores[1])
            arena->winner = 1;
        else
            arena->winner = -1;
    }
}

int arena_apply_command(arena_t *arena, command_t command)
{
    if (arena->game_over)
        panic("Trying to apply command to a finished game");

    turn_info_t info = get_turn_info(arena, command.direction);

    if (!is_bee(info.bee_type) || info.player != cell_player(info.bee_type))
        panic("Invalid bee");

    int success = action_functions[command.action](arena, command.direction);
    check_endgame(arena);
    arena->turn++;

    return success;
}
