local runs_per_match = 10

if #arg ~= 3 then
    print "Usage: lua compo.lua day team1_directory team2_directory"
    os.exit(1)
end

local day = arg[1]
local team1, team2 = arg[2], arg[3]
local dir = "day" .. day .. "-" .. team1 .. "-" .. team2

os.execute("mkdir -p " .. dir)

for i = 1, runs_per_match do
    print "==========="
    print("Run " .. i)
    local cmd = string.format("./match.sh %s %s %s", team1, team2, dir)
    os.execute(cmd)

end

------

local report = {}
local counts = {}

table.insert(report, string.format("## Day %s: %s vs. %s", day, team1, team2))
table.insert(report, "")

for file in io.popen("ls " .. dir):lines() do
    if file:match ".log.gz" then
        local path = dir.. '/' .. file
        local result = io.popen("tools/result " .. path):read("a")

        local line = string.format("- [%s](%s)", file, path)

        local winner = result:match "Winner.*%((.*)%)"
        if winner then
            counts[winner] = (counts[winner] or 0) + 1
            line = line .. " | " .. winner
        end

        table.insert(report, line)
    end
end

table.insert(report, "")

local flat = {}
for k,v in pairs(counts) do
    table.insert(flat, k)
end

table.sort(flat, function(a,b) return counts[a] < counts[b] end)

if #flat == 0 then
    table.insert(report, "Draw")
elseif #flat == 1 then
    table.insert(report, flat[1])
elseif #flat == 2 then
    if counts[flat[1]] == counts[flat[2]] then
        table.insert(report, "Draw")
    else
        table.insert(report, "**" .. flat[2] .. "**")
    end
end

table.insert(report, "")

local fp = io.open("day" .. day .. ".md", "a")
for i,v in pairs(report) do
    fp:write(v, '\n')
end
fp:close()
