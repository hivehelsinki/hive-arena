local agent = require "libagent"
local dirs = {'N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW'}

function find_neighbour(cells, cell_type)

    for _,dir in ipairs(dirs) do

        local row,col = agent.direction_to_offset(dir)
        if cells[row][col] == cell_type then
            return dir
        end
    end

    return nil
end

function think(info)

    local bee_cell = info.cells[0][0]

    local has_flower = bee_cell:match "WITH_FLOWER"

    if has_flower then
        local hive_dir = find_neighbour(info.cells, "HIVE_" .. info.player)
        if hive_dir then
            return {
                action = "FORAGE",
                direction = hive_dir
            }
        end

    else
        local flower_dir = find_neighbour(info.cells, "FLOWER")
        if flower_dir then
            return {
                action = "FORAGE",
                direction = flower_dir
            }
        end
    end

    local dir = dirs[math.random(8)]

    return {
        action = "MOVE",
        direction = dir
    }
end

function main(args)

    if #args ~= 2 then
        print "Usage: ./agent arena_host arena_port"
        os.exit(1)
    end

    math.randomseed(os.time())

    local host = args[1]
    local port = tonumber(args[2])
    local team_name = "example_agent_lua"

    agent.main(host, port, team_name, think)
end

main({...})
