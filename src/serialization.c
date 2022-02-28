#include <stdlib.h>
#include <string.h>
#include "serialization.h"

static const char *agent_info_format = "%d,%d,%d,%d,%d,%s";
static const char *command_info_format = "%d,%d";

void serialize_agent_info(agent_info_t info, char *buffer)
{
    char cells[VIEW_SIZE * VIEW_SIZE + 1];

    for(int row = 0 ; row < VIEW_SIZE ; row++)
    {
        for(int col = 0 ; col < VIEW_SIZE ; col++)
        {
            char c = '0' + (int) info.cells[row][col];
            if (c > '9')
                panic("Serialization fail");

            cells[row * VIEW_SIZE + col] = c;
        }
    }
    cells[VIEW_SIZE * VIEW_SIZE] = '\0';

    sprintf(buffer,
        agent_info_format,
        info.turn,
        info.player,
        info.bee,
        info.row,
        info.col,
        cells
    );
}

agent_info_t deserialize_agent_info(const char *buffer)
{
    agent_info_t info;
    char cells[MAX_AGENT_INFO_LEN];

    if (strlen(buffer) >= MAX_AGENT_INFO_LEN)
        panic("Deserialization fail 0");

    sscanf(buffer, agent_info_format,
        &info.turn,
        &info.player,
        &info.bee,
        &info.row,
        &info.col,
        cells
    );

    if (strlen(cells) != VIEW_SIZE * VIEW_SIZE)
        panic("Deserialization fail 1");

    for(int row = 0 ; row < VIEW_SIZE ; row++)
    {
        for(int col = 0 ; col < VIEW_SIZE ; col++)
        {
            char c = cells[row * VIEW_SIZE + col];
            if (c < '0' || c > '9')
                panic("Deserialization fail 2");

            info.cells[row][col] = (cell_t) (c - '0');
        }
    }

    if(1)
    {
        char buffer2[MAX_AGENT_INFO_LEN];
        serialize_agent_info(info, buffer2);
        if(strcmp(buffer, buffer2) != 0)
            panic("Deserialization unit test fail");
    }

    return info;
}

void serialize_agent_command(command_t command, char *buffer)
{
    sprintf(buffer,
        command_info_format,
        command.action,
        command.direction
    );
}

int deserialize_agent_command(char *buffer, command_t *command)
{
    int action_int, dir_int;
    int status = sscanf(buffer, command_info_format, &action_int, &dir_int);

    if (status == EOF || action_int < 0 || action_int > GUARD
        || dir_int < 0 || dir_int > NW)
    {
        return 0;
    }

    command->action = (action_t) action_int;
    command->direction = (dir_t) dir_int;

    return 1;
}

void log_turn(arena_t *arena, FILE *log, command_t command)
{
    int player, bee;
    turn_to_player_and_bee(arena->turn, &player, &bee);

    fprintf(log, "%d,%d,%d,", arena->turn, player, bee);
    fprintf(log, "%d,%d,", arena->scores[0], arena->scores[1]);

    for(int row = 0 ; row < NUM_ROWS ; row++)
    {
        for(int col = 0 ; col < NUM_COLS ; col++)
        {
            fprintf(log, "%c", arena->cells[row][col] + '0');
        }
    }

    fprintf(log, ",%d,%d\n", command.action, command.direction);
}
