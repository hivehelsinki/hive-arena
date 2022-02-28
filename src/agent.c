#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <string.h>
#include <sys/socket.h>
#include <arpa/inet.h>

#include "arena_common.h"
#include "serialization.h"
#include "netutils.h"

static int connect_to_arena(char *host, int port)
{
    int sock = socket(PF_INET , SOCK_STREAM , 0);
    if (sock == -1)
        panic("Could not create socket");

    struct sockaddr_in server_address;
    server_address.sin_addr.s_addr = inet_addr(host);
    server_address.sin_family = AF_INET;
    server_address.sin_port = htons(port);

    int status = connect(sock, (struct sockaddr *) &server_address, sizeof(server_address));
    if (status < 0)
        panic("Could not connect to arena");

    return sock;
}

static void send_team_name(int socket, char *name)
{
    int len = strlen(name);
    if (len + 1 > NET_BUFFER_SIZE)
        panic("Team name is too long");
    char buffer[len + 1];

    strcpy(buffer, name);
    buffer[len] = '\n';

    ssize_t sent = send(socket, buffer, len + 1, 0);
    if (sent <= 0)
        panic("Could not send team name to arena");
}

static void close_socket(int socket)
{
    close(socket);
}

static int get_agent_info(int socket, agent_info_t *info)
{
    char *line = get_line_from_socket(socket);
    if (!line)
        panic("Lost connection to arena");

    if (strcmp(line, "gameover") == 0)
        return 0;

    *info = deserialize_agent_info(line);

    free(line);
    return 1;
}

static void send_agent_command(command_t command, int socket)
{
    char buffer[MAX_COMMAND_LEN];

    serialize_agent_command(command, buffer);
    int len = strlen(buffer);
    buffer[len] = '\n';
    len++;

    ssize_t sent = send(socket, buffer, len, 0);
    if (sent <= 0)
        panic("Could not send command to arena");
}

static void run(int socket, command_t (*think_function)(agent_info_t))
{
    while(1)
    {
        agent_info_t info;

        int success = get_agent_info(socket, &info);
        if (!success)
        {
            puts("Game over");
            break;
        }

        command_t command = think_function(info);
        send_agent_command(command, socket);
    }
}

int agent_main(char *host, int port, char *team_name, command_t (*think_function)(agent_info_t))
{
    int socket = connect_to_arena(host, port);

    send_team_name(socket, team_name);
    run(socket, think_function);

    close_socket(socket);
    return 0;
}
