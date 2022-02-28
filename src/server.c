#include <sys/socket.h>
#include <arpa/inet.h>
#include <sys/time.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <string.h>

#include "server.h"
#include "serialization.h"
#include "netutils.h"

server_t start_server(int port, char *logfile)
{
    server_t server;
    int status;

    server.socket = socket(PF_INET, SOCK_STREAM, 0);
    if (server.socket == -1)
        panic("Could not create server socket");

    const int true = 1;
    status = setsockopt(server.socket, SOL_SOCKET, SO_REUSEADDR, &true, sizeof(true));
    if (status < 0)
        panic("Could not set server socket to reuse address");

    struct sockaddr_in server_address = {
        .sin_family = AF_INET,
        .sin_addr.s_addr = INADDR_ANY,
        .sin_port = htons(port)
    };

    status = bind(server.socket, (struct sockaddr *) &server_address, sizeof(server_address));
	if (status < 0)
        panic("Could not bind server socket");

    status = listen(server.socket, 2);
    if (status < 0)
        panic("Could not set server socket to listen");

    for(int client_id = 0 ; client_id < 2 ; client_id++)
    {
        int client_socket;
        struct sockaddr_in client_address;
        socklen_t addr_len;

        client_socket = accept(server.socket, (struct sockaddr *) &client_address, &addr_len);
        if (client_socket == -1)
            panic("Error while accepting client connection");

        char str[INET_ADDRSTRLEN];
        inet_ntop(AF_INET, &client_address.sin_addr.s_addr, str, INET_ADDRSTRLEN);

        printf("Connection from %s\n", str);

        struct timeval timeout;
        timeout.tv_sec = 2;
        timeout.tv_usec = 0;

        status = setsockopt(client_socket, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout));
        if (status < 0)
            panic("Could not set timeout on client socket");

        char *line = get_line_from_socket(client_socket);
        if (!line)
        {
            printf("Client did not send their name\n");
            exit(1);
        }

        printf("Client sent name: %s\n", line);

        server.clients[client_id] = (client_t) {
            .socket = client_socket,
            .name = line
        };
    }

    FILE *log = fopen(logfile, "w");
    if (!log)
        panic("Could not open log file");

    server.log = log;

    return server;
}

int send_agent_info(agent_info_t info, int socket)
{
    char buffer[MAX_AGENT_INFO_LEN];

    serialize_agent_info(info, buffer);
    int len = strlen(buffer);

    buffer[len] = '\n';
    len++;

    ssize_t sent = send(socket, buffer, len, 0);
    if (sent <= 0)
        return 0;

    return 1;
}

int get_agent_command(int socket, command_t *command)
{
    char *line = get_line_from_socket(socket);
    if(!line)
        return 0;

    int success = deserialize_agent_command(line, command);
    free(line);

    return success;
}

static void send_game_over(int socket)
{
    char *str = "gameover\n";
    send(socket, str, strlen(str), 0);
}

void stop_server(server_t *server)
{
    for(int i = 0 ; i < 2 ; i++)
    {
        send_game_over(server->clients[i].socket);
        close(server->clients[i].socket);
        free(server->clients[i].name);
    }

    close(server->socket);
}
