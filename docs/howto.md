How to use
==========

Requirements
------------

Requirements for the arena and the agent library:

- a POSIX compliant operating system (tested on macOS, Linux Mint, WSL Ubuntu)
- a C compiler accepting the C99 standard (tested with gcc and clang)
- make

Requirements for the match viewer:

- a POSIX compliant operating system
- Lua 5.3 or later (on macOS, the easiest way to install it is `brew install lua`. See [brew](https://brew.sh/).)

Requirements for the extra agents are detailed in their respective directories.

Local match
-----------

A script is provided to easily run a match locally on your machine. This script is representative of how the various programs will be invoked during the competition.

Run `./match.sh player0_directory player1_directory`.

This will build the arena, the player's agents, and run them locally on the machine.

A log in the format `p0-p1-date.log.gz` will be created and automatically viewed with the viewer tool. You may also view the log at a later time using

`tools/viewer logfile`

Quickstart: how to write an agent?
----------------------------------

Duplicate and rename the `example-agent` directory, and edit `main.c`. That's it! See the `libagent` documentation for details on the API.

Using the provided library requires no extra knowledge of the internal structure or protocol :)

Extra details
=============

Arena
-----

The `arena` is a program that sets up the game, implements the rules, and expects the players' agents to connect to it over standard network connections (TCP).

Usage: `bin/arena port logfile`

On most UNIX systems, ports below 1024 can only be used by administrators. The `match.sh` script uses 8000 by default.

An agent program should connect to the server, send a team name and get ready to answer each message from the server with a response command. The complete protocol is details in its [own document](protocol.md).

Note: you do not need to implement this protocol yourself if you use the provided library!

Agent
-----

A source directory for an agent should:

- be named after the team who wrote it :)
- contain a makefile so that invoking `make` without arguments builds the project. It may do nothing, but should be present and have at least one rule, in order to report success.
- after running `make`, contain an executable file named `agent` (mind the permissions)

The `agent` executable will be invoked as follows:

`cd team_directory; ./agent arena_host arena_port`

That is, it will take in the hostname or IP address of the arena, and the port to connect to.

You are free to implement your agent in any language you want, using any framework you want, as long as it's ready to go with a single `make` invocation! Make sure to include all your dependencies in your submission.

A C library is provided (see `libagent` and `example-agent`), and examples in Lua and Python as well (`example-agent-*`). To use another language, you can use these as models to implement the protocol yourself.
