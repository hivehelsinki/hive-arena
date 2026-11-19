local http = require "http.request"
local json = require "lunajson"

local args = {...}
local host = args[1]

local headers, stream = http.new_from_uri("http://" .. host .. "/newgame?players=4&map=random"):go()
local info = json.decode(stream:get_body_as_string())

print("Started game " .. info.id, info.adminToken)

os.execute(string.format("go run ../viewer --host %s --id %s --token %s &", host, info.id, info.adminToken))

for i = 1,4 do
	os.execute(string.format("go run . %s %s %s %s",
		host,
		info.id,
		"Team" .. i,
		i == 4 and "" or " >> /dev/null &"
	))
end
