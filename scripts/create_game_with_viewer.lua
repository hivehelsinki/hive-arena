local http = require "http.request"
local json = require "lunajson"

local args = {...}

if #args < 3 then
	print("Usage: lua scripts/create_game_with_viewer.lua <host> <numPlayers> <map>")
	return
end

local host = args[1]
local numPlayers = tonumber(args[2])
local map = args[3]

local headers, stream = http.new_from_uri("http://" .. host .. "/newgame?players=".. numPlayers .. "&map=" .. map):go()
local info = json.decode(stream:get_body_as_string())

print("Started game " .. info.id, info.adminToken)

while true do
	os.execute(string.format("go run ./viewer --host %s --id %s --token %s", host, info.id, info.adminToken))
	print("Press enter to restart viewer...")
	_ = io.read(1)
end
