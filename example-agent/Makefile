CC=gcc
CFLAGS=-Wall -Wextra -O3 -flto
CPPFLAGS=-I../src
LDFLAGS=-flto -L../bin -lagent

OBJS=main.o
NAME=agent

all: $(NAME)

$(NAME): $(OBJS)
	$(CC) -o $(NAME) $(OBJS) $(LDFLAGS)

clean:
	rm -f $(OBJS)

fclean: clean
	rm -f $(NAME)

re: fclean all
