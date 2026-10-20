local json = require "lunajson"		-- luarocks install lunajson

local args = {...}
local txt

if #args == 1 then
	txt = io.open(args[1]):read("a")
else
	txt = io.stdin:read("a")
end

local data = json.decode(txt)

local top,bottom,left,right

if not data.hexes then
	data = data.state
end

local function parseCoords(c)
	local row,col = c:match("(%d+),(%d+)")
	return tonumber(row), tonumber(col)
end

for coords,v in pairs(data.hexes) do
	local row, col = parseCoords(coords)
	top = top and math.min(top, row) or row
	bottom = bottom and math.max(bottom, row) or row
	left = left and math.min(left, col) or col
	right = right and math.max(right, col) or col
end

local lines = {}
for i = top, bottom do
	lines[i] = {}
end

local terrainToChar = {
	EMPTY = ".",
	ROCK = "R",
	FIELD = "F"
}

local entityToChar = {
	BEE = "B",
	HIVE = "H",
	WALL = "W"
}

local totalResources = 0

for coords,hex in pairs(data.hexes) do
	local c = terrainToChar[hex.terrain]
	local row, col = parseCoords(coords)
	lines[row][col] = c

	if hex.resources then
		totalResources = totalResources + hex.resources
	end
end

for coords,hex in pairs(data.hexes) do
	local row, col = parseCoords(coords)
	local entity = hex.entity

	if entity then
		local c = entityToChar[entity.type] .. entity.player
		lines[row][col] = c
	elseif hex.influence >= 0 then
		lines[row][col] = tostring(hex.influence)
	end
end

for row = top,bottom do
	for col = left,right do
		local s = lines[row][col] or " "
		io.write(s .. string.rep(" ", 2 - #s))
	end
	io.write "\n"
end

print("Turn: ", data.turn)
print("Last influence change: ", data.lastInfluenceChange)
print("Resources: ", table.concat(data.playerResources, ", "))
print("Resources left on map: ", totalResources)
print("Game over:", data.gameOver)
if (data.gameOver) then
	print("Winners:", json.encode(data.winners))
end
