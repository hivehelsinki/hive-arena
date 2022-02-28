local lsock = require "socket"
local constants = require "constants"

local actions_to_ints = {}
for k,v in pairs(constants.action_names) do
    actions_to_ints[v] = k
end

local directions_to_ints = {}
for k,v in pairs(constants.direction_names) do
    directions_to_ints[v] = k
end

local offsets = {
    N = {-1, 0},
    NE = {-1, 1},
    E = {0, 1},
    SE = {1, 1},
    S = {1, 0},
    SW = {1,-1},
    W = {0,-1},
    NW = {-1,-1}
}

local function direction_to_offset(dir)
    return table.unpack(offsets[dir])
end

local function panic(message)
    print(message)
    os.exit(1)
end

local function open_socket(host, port, name)

    local socket = lsock.connect(host, port)
    if not socket then
        panic("Could not connect to " .. host .. ":" .. port)
    end

    socket:send(name .. '\n')

    return socket
end

local function get_agent_info(socket)

    local line, err = socket:receive('*l')
    if not line then
        panic("Lost connection to arena")
    end

    if line == "gameover" then
        return nil
    end

    local turn, player, bee, row, col, cell_chars =
        line:match("^(%d+),(%d+),(%d+),(%d+),(%d+),(%d+)$")

    local cells = {}
    local index = 1
    for row = -constants.VIEW_DISTANCE, constants.VIEW_DISTANCE do
        cells[row] = {}
        for col = -constants.VIEW_DISTANCE, constants.VIEW_DISTANCE do
            local number = tonumber(cell_chars:sub(index,index))
            cells[row][col] = constants.cell_values[number]

            index = index + 1
        end
    end

    return {
        turn = tonumber(turn),
        player = tonumber(player),
        bee = tonumber(bee),
        row = tonumber(row),
        col = tonumber(col),
        cells = cells
    }
end

local function send_agent_command(socket, command)

    local action_int = actions_to_ints[command.action]
    local direction_int = directions_to_ints[command.direction]

    if not action_int or not direction_int then
        panic("Invalid command: " .. command.action .. "," .. command.direction)
    end

    local str = action_int .. "," .. direction_int .. '\n'

    local success = socket:send(str)
    if not success then
        panic("Lost connection to arena")
    end
end

local function main(host, port, team_name, think_function)

    if #team_name > 199 then
        panic("Team name is too long")
    end

    local socket = open_socket(host, port, team_name)

    while true do

        local info = get_agent_info(socket)

        if not info then
            print "Game over"
            break
        end

        local command = think_function(info)
        send_agent_command(socket, command)
    end

end

return
{
    main = main,
    constants = constants,
    direction_to_offset = direction_to_offset
}
