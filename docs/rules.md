Game rules
==========

Arena
-----

The game is played in a rectangular arena (25 rows, 30 columns).

Types of cells:

- Empty
- Flower
- Wall
- Hive (one for each team)
- Bee (5 for each team)
    - May be carrying a flower

The hive for player 0 is in the middle row, column 1, and their bees start from the five middle rows of column 2. Player 1 starts with a symmetrical setup on the other side of the arena.

At the start of the game, the rest of the arena is randomly filled with flowers (between 30 and 40 flowers).

Gameplay
--------

Start:

- Participants are randomly assigned to player 0 and player 1.
- The game is played in turns, alternating between the two players.
- Player 0 plays first.

Turns:

- At each turn, the next bee for the player is activated. A whole round consists therefore of 10 turns:
    - Player 0, bee 0
    - Player 1, bee 0
    - Player 0, bee 1
    - Player 1, bee 1
    - Player 0, bee 2
    - Player 1, bee 2
    - Player 0, bee 3
    - Player 1, bee 3
    - Player 0, bee 4
    - Player 1, bee 4

On its turn, a bee receives information about its surrounding and state:

- the current turn, current player, and active bee
- its position
- the contents of the cells within a distance of 3

It must return one command, made of an action and a direction.

Directions are N, NE, E, SE, S, SW, W, NW, with:

- S is increasing row number
- E is increasing column number

Actions always involve the adjacent cell in a given direction. The four possible actions are:

- move: move one step in the given direction. If the destination is not empty, nothing happens.
- forage:
    - if the bee is not holding a flower, pickup a flower from the given direction. If the cell does not contain a flower, nothing happens.
    - if the bee is currently holding a flower, drop it into the direction. If the destination is empty, it becomes a flower cell. If the destination is a hive, the team owning the hive marks a point. In both cases, the bee loses the flower. In any other situation, nothing happens.
- build: build a wax wall in the given direction. If the cell is not empty, nothing happens.
- guard:
    - If the destination is a wax wall or a flower, it is destroyed with 50% chance, and the cell becomes empty.
    - If the destination is a bee carrying a flower, the flower is dropped with 50% chance in a random empty adjacent cell (unless there are none).
    - Otherwise, nothing happens.

Additionally, a bee carrying a flower:

- has a 33% chance of failing a move action

Endgame
-------

When there are no more flowers in the arena (either on the ground or being carried), the game ends.

Alternatively, if 1000 turns go by without a single point being earned, the end game is engaged. The game continues until the end of the round, and then ends (ensuring all bees have acted the same number of times).

The team with most flowers dropped in their hive wins the game. If both teams collected the same amount of flowers, the game ends in a draw.
