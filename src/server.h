#ifndef SERVER_H
#define SERVER_H

#include "arena_common.h"

typedef struct
{
    int socket;
    char *name;
} client_t;

typedef struct
{
    int socket;
    client_t clients[2];
    FILE *log;
} server_t;

server_t start_server(int port, char *logfile);
int send_agent_info(agent_info_t info, int socket);
int get_agent_command(int socket, command_t *command);
void stop_server(server_t *server);

#endif // SERVER_H
