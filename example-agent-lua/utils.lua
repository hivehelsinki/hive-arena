local function parseCoords(c)
	local row,col = c:match("(%d+),(%d+)")
	return tonumber(row), tonumber(col)
end

local function printState(state)

	local top,bottom,left,right

	for coords,v in pairs(state.hexes) do
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

	for coords,hex in pairs(state.hexes) do
		local c = terrainToChar[hex.terrain]
		local row, col = parseCoords(coords)
		lines[row][col] = c

		if hex.resources then
			totalResources = totalResources + hex.resources
		end
	end

	for coords,hex in pairs(state.hexes) do
		local row, col = parseCoords(coords)
		local entity = hex.entity

		if entity then
			local c = entityToChar[entity.type] .. entity.player
			lines[row][col] = c
		end
	end

	for row = top,bottom do
		for col = left,right do
			local s = lines[row][col] or " "
			io.write(s .. string.rep(" ", 2 - #s))
		end
		io.write "\n"
	end

	print("Turn: ", state.turn)
	print("Last resource change: ", state.lastResourceChange)
	print("Resources: ", table.concat(state.playerResources, ", "))
	print("Resources left on map: ", totalResources)
	print("Game over:", state.gameOver)
	if (state.gameOver) then
		print("Winners:", table.concat(state.winners, ", "))
	end

end

return {
	parseCoords = parseCoords,
	printState = printState
}
