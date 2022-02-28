import sys
import libagent
import random

def find_neighbour(cells, cell_type):
    for dir in libagent.direction_names:
        (row,col) = libagent.directions_to_offsets[dir]
        if cells[row][col] == cell_type:
            return dir

    return None


def think(info):
    cells = info["cells"]
    bee_cell = cells[0][0]
    has_flower = "WITH_FLOWER" in bee_cell

    if has_flower:
        hive_dir = find_neighbour(cells, "HIVE_%d" % info["player"])
        if hive_dir:
            return {
                'action': "FORAGE",
                'direction': hive_dir
            }

    else:
        flower_dir = find_neighbour(cells, "FLOWER")
        if flower_dir:
            return {
                'action': "FORAGE",
                'direction': flower_dir
            }

    return {
        'action': "MOVE",
        'direction': random.choice(libagent.direction_names)
    }

def main():

    if len(sys.argv) != 3:
        print("Usage: ./agent arena_host arena_port")
        exit(1)

    libagent.main(sys.argv[1], int(sys.argv[2]), "example-agent-python", think)


main()
