#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <unistd.h>
#include "arena.h"
#include "serialization.h"
#include "server.h"

static void shuffle_players(arena_t *arena, server_t *server)
{
    if (arena_random(arena, 2) == 0)
    {
        client_t tmp = server->clients[0];

        server->clients[0] = server->clients[1];
        server->clients[1] = tmp;
    }
}

void run_game(arena_t *arena, server_t *server)
{
    shuffle_players(arena, server);

    fprintf(server->log, "# Player 0: %s\n", server->clients[0].name);
    fprintf(server->log, "# Player 1: %s\n", server->clients[1].name);

    while(!arena->game_over)
    {
        int status;
        int player, bee;
        turn_to_player_and_bee(arena->turn, &player, &bee);

        int socket = server->clients[player].socket;

        agent_info_t info = get_bee_point_of_view(arena);
        status = send_agent_info(info, socket);
        if (!status)
        {
            fprintf(server->log, "# Player %d timeout\n", player);
            arena->winner = (player + 1) % 2;
            break;
        }

        command_t command;
        status = get_agent_command(socket, &command);
        if (!status)
        {
            fprintf(server->log, "# Player %d timeout/bad command\n", player);
            arena->winner = (player + 1) % 2;
            break;
        }

        log_turn(arena, server->log, command);
        arena_apply_command(arena, command);
    }

    if (arena->game_over)
        log_turn(arena, server->log, (command_t) {-1,-1});
    
    if (arena->winner != -1)
    {
        fprintf(server->log, "# Winner: player %d (%s)\n",
            arena->winner,
            server->clients[arena->winner].name
        );
    }
    else
    {
        fprintf(server->log, "# Draw\n");
    }
}

int main(int argc, char **argv)
{
    if (argc < 3)
        panic("Usage: ./arena port logfile");

    int port = atoi(argv[1]);
    char *logfile = argv[2];

    int seed = time(NULL);
    arena_t *arena = arena_new(seed);
    server_t server = start_server(port, logfile);

    run_game(arena, &server);

    stop_server(&server);
    arena_delete(arena);

    return 0;
}
