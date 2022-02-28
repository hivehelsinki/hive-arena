CC=gcc
CFLAGS=-Wall -Wextra -Werror -O3 -flto
LDFLAGS=-flto
ARENA_DEPS=arena.o game.o actions.o arena_common.o serialization.o server.o netutils.o
LIBAGENT_DEPS=agent.o serialization.o arena_common.o netutils.o
BUILD_DIR=../bin

all: arena libagent.a install

install: arena libagent.a
	mkdir -p $(BUILD_DIR)
	cp arena libagent.a $(BUILD_DIR)

arena: $(ARENA_DEPS)
	$(CC) -o arena $(LDFLAGS) $(ARENA_DEPS)

libagent.a: $(LIBAGENT_DEPS)
	ar rcs libagent.a $(LIBAGENT_DEPS)

clean:
	rm -f $(ARENA_DEPS) $(LIBAGENT_DEPS)

fclean: clean
	rm -fr arena libagent.a $(BUILD_DIR)

re: fclean all
