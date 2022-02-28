#ifndef AGENT_H
#define AGENT_H

#include "arena_common.h"

int agent_main(char *host, int port, char *team_name, command_t (*think_function)(agent_info_t));

#endif // AGENT_H
