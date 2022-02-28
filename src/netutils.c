#include <stdlib.h>
#include <sys/socket.h>
#include <arpa/inet.h>

#include "netutils.h"

char *get_line_from_socket(int socket)
{
    char *line = (char *)malloc(NET_BUFFER_SIZE * sizeof(char));

    ssize_t length = recv(socket, line, NET_BUFFER_SIZE, 0);
    if (length <= 0 || line[length - 1] != '\n')
    {
        free(line);
        return NULL;
    }

    line[length - 1] = '\0';
    return line;
}
