local arena = require "arena"
local utils = require "utils"

local args = {...}

-- Arguments, or environment variables when run by the ladder (see docs/docker.md)
local host = args[1] or os.getenv "ARENA_HOST"
local gameid = args[2] or os.getenv "ARENA_GAME_ID"
local name = args[3] or os.getenv "ARENA_NAME"

if not host or not gameid or not name then
	print "Usage: lua main.lua <host> <gameid> <name>"
	print "or set the ARENA_HOST, ARENA_GAME_ID and ARENA_NAME environment variables"
	os.exit(1)
end

local function makeOrders(state, player)

	utils.printState(state)

	local orders = {}

	for coords,hex in pairs(state.hexes) do
		if hex.entity and hex.entity.type == "BEE" and hex.entity.player == player then

			local order = {
				type = "MOVE",
				coords = coords,
				direction = ({"NE", "E", "SE", "SW", "W", "NW"})[math.random(1,6)]
			}

			table.insert(orders, order)
		end
	end

	return orders
end

arena.runAgent(host, gameid, name, makeOrders)
