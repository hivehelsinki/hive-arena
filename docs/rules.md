# Game rules

The game is a simple strategy game, similar to Warcraft, Starcraft or Command and Conquer. Players build buildings, spawn units, gather resources and compete to achieve a particular goal: most resources collected.

The theme is hives, bees and flowers, obviously :)

The game is played in simultaneous turns: players get information about their environment, then, all players submit commands at the same time, which are processed together to update the game state for the next turn.

## Map

The game is played on a hexagonal tiling of cells. Pre-written map files indicate:

- static terrain
- starting positions for players' buildings and units

### Static terrain

Terrain can be of three types:

- rock (impassable and cannot be built in)
- empty
- flower fields, from which resources (flowers) can be foraged

The terrain is static and cannot change throughout the duration of the game.

### Dynamic entities

Players start the game with, and can create entities of three types:

- wax wall, a building that does not move, and simply blocks movement
- hive, a building that does not move, and can create more bees
- bee, a mobile unit which can perform most actions in the game (a bee can additionally be carrying a flower)

There can only ever be one entity per cell, which therefore prevents movement or additional building into that cell.

## Game state

In addition to entities, the game state consists in:

- the number of remaining flowers in each flower field of the map
- the number of flowers in reserves for each player

## Gameplay

### Start

At the start of the game, players' starting units and buildings are placed in the possible spawning points as determined by the map.

### Field of view

Before each turn, all player agents receive from the server a partial view of the map: all terrain and entities that are within a fixed distance from their own hives and bees.

### Commands

Then, players' agents are expected to *simultaneously* submit commands for all their units and buildings. The order of commands is significant.

Commands are applied in rounds, as follows: in round N, the Nth commands of all players (if any) are gathered and executed in a random order.

That is, each players' commands will be executed in the order they are submitted, but interleaved randomly with the other players' for fairness. This gives players an incentive to place their most critical commands towards the beginning of their list.

Each unit or building can only perform a single action in a turn: subsequent attempts fail.

The possible commands for bees are the following:

- `move`: move one step in a given direction.
- `forage`: if the bee is not currently carrying a flower, gather one flower from the field it is currently in. The field's flowers are reduced by one, and the bee is now carrying a flower. If the bee is not in a field, or that field is empty, the command fails.
If the bee is already carrying a flower, and is adjacent to a hive of the same player, the player's resources are immediately increased by one, and the bee is not longer carrying a flower. If the bee is not adjacent to a hive of the same player, the command fails.
- `build wall`: create a wax wall in the given direction.
- `build hive`: transform the bee into a hive in its current cell.
- `attack`: attack the adjacent entity in the given direction. If it is a wax wall, it is destroyed with a 1 in 6 chance. If it is a bee, it is stunned with a 1 in 2 chance, and cannot act for the rest of the turn (nothing happens if it has already acted).

The possible commands for hives are:

- `spawn bee`: create a new bee in the given direction.

The commands `move`, `build wall` and `spawn bee` all take a direction as parameter: they target the adjacent cell in the given direction. If that cell is blocked (the terrain is stone, or it contains an entity already), the command fails.

The commands `build wall`, `build hive` and `spawn bee` all have a cost in resources (flowers): the player's resources are immediately reduced by that cost. If the player does not have enough resources to pay that cost, the command fails.

### Victory conditions

The game ends when all flower fields are depleted and no bee is carrying flowers. The winner is the player with most flowers currently in reserves (or all tied for most).

The game also ends if no flower has been dropped into a hive in a given number of turns.

## Hardcoded values

|          | Cost |
|----------|------|
| Bee      | 6    |
| Hive     | 12   |
| Wax wall | 1    |

- Flower field initial content: 8 flowers.
- Field of view: 4 hexes away.
- Resource timeout: 50 turns.
