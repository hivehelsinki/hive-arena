local function genRect(w,h)
	for row = 1, h do
		if row % 2 == 0 then
			io.write "  "
		end

		for col = 1, w do
			io.write ".   "
		end

		io.write "\n"
	end
end

local function genHex(w)
	for row = 1, 2 * w - 1 do
		local offset = math.abs(w - row)

		if row % 2 == 0 then
			io.write "  "
		end

		io.write(string.rep("    ", (offset + 1 - w % 2) // 2))
		io.write(string.rep(".   ", 2 * w - 1 - offset))

		io.write "\n"
	end
end

local args = {...}

if args[1] == "rect" then
	genRect(args[2], args[3])
elseif args[1] == "hex" then
	genHex(args[2])
end
