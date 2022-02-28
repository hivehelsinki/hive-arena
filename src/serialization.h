#ifndef SERIALIZATION_H
#define SERIALIZATION_H

#include <stdio.h>
#include "arena_common.h"
#include "arena.h"

#define MAX_AGENT_INFO_LEN (VIEW_SIZE * VIEW_SIZE + 30)
#define MAX_COMMAND_LEN (10)

void serialize_agent_info(agent_info_t info, char *buffer);
agent_info_t deserialize_agent_info(const char *buffer);

int deserialize_agent_command(char *buffer, command_t *command);
void serialize_agent_command(command_t command, char *buffer);

void log_turn(arena_t *arena, FILE *log, command_t command);

#endif // SERIALIZATION_H
