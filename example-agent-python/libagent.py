import socket
import re

NUM_ROWS = 25
NUM_COLS = 30
VIEW_DISTANCE = 3

action_names = ["MOVE", "FORAGE", "BUILD", "GUARD"]
direction_names = ["N", "NE", "E", "SE", "S", "SW", "W", "NW"]
cell_values = [
    "EMPTY",
    "BEE_0",
    "BEE_1",
    "BEE_0_WITH_FLOWER",
    "BEE_1_WITH_FLOWER",
    "FLOWER",
    "WALL",
    "HIVE_0",
    "HIVE_1",
    "OUTSIDE"
]

actions_to_ints = {}
for i, action in enumerate(action_names):
    actions_to_ints[action] = i

directions_to_ints = {}
for i, direction in enumerate(direction_names):
    directions_to_ints[direction] = i

directions_to_offsets = {
    "N": (-1, 0),
    "NE": (-1, 1),
    "E": (0, 1),
    "SE": (1, 1),
    "S": (1, 0),
    "SW": (1,-1),
    "W": (0,-1),
    "NW": (-1,-1)
}

def panic(message):
    print(message)
    exit(1)

def open_socket(host, port, team_name):
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.connect((host, port))
        sock.send((team_name + '\n').encode())
    except:
        panic("Could not connect to %s:%d" % (host, port))

    return sock

def get_agent_info(sock):
    line = sock.recv(1024).decode().rstrip('\n')
    if line == "gameover":
        return None

    match = re.findall(r'\d+', line)
    cell_chars = match[5]

    cells = {}
    index = 0
    for row in range(-VIEW_DISTANCE, VIEW_DISTANCE+1):
        cells[row] = {}
        for col in range(-VIEW_DISTANCE, VIEW_DISTANCE+1):
            number = int(cell_chars[index])
            cells[row][col] = cell_values[number]
            index += 1

    return {
        "turn": int(match[0]),
        "player": int(match[1]),
        "bee": int(match[2]),
        "row": int(match[3]),
        "col": int(match[4]),
        "cells": cells
    }

def send_agent_command(sock, command):
    try:
        action_int = actions_to_ints[command['action']]
        direction_int = directions_to_ints[command['direction']]
    except:
        panic("Invalid command: %s" % command)

    line = "%d,%d\n" % (action_int, direction_int)

    try:
        sock.sendall(line.encode())
    except:
        panic("Lost connection to arena")


def main(host, port, team_name, think_function):
    if len(team_name) > 199:
        panic("Team name is too long")

    sock = open_socket(host, port, team_name)

    while True:
        info = get_agent_info(sock)
        if info == None:
            print("Game over")
            break

        command = think_function(info)
        send_agent_command(sock, command)
