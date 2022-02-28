local term = require "plterm"
local constants = require "constants"

function get_players_names(fp)

    local line, err = fp:read('l')
    if not line then
        return nil
    end

    local p0 = line:match("# Player 0: (.*)")

    line, err = fp:read('l')
    if not line then
        return nil
    end
    local p1 = line:match("# Player 1: (.*)")

    return p0, p1
end

function read_next_turn(fp)

    local line, err = fp:read('l')
    if not line then
        return nil
    end

    local message = line:match("^# (.*)")
    if message then
        return {
            message = message
        }
    end

    local turn, player, bee, score0, score1, cells, action, direction =
        line:match("^(%d+),(%d+),(%d+),(%d+),(%d+),(%d+),(%-?%d+),(%-?%d+)$")

    local converted_cells = {}
    for i = 1,#cells do
        local num = cells:sub(i,i):byte() - string.byte('0')
        converted_cells[i] = constants.cell_values[num]
    end

    local data = {
        turn = tonumber(turn),
        player = tonumber(player),
        bee = tonumber(bee),
        score0 = tonumber(score0),
        score1 = tonumber(score1),
        cells = converted_cells,
        action = constants.action_names[tonumber(action)],
        direction = constants.direction_names[tonumber(direction)]
    }

    return data
end

function load_log(path)

    local fp = io.open(path, "r")
    if not fp then
        panic("Could not open " .. path)
    end

    local p0, p1 = get_players_names(fp)

    local turns = {}

    while true do
        local turn = read_next_turn(fp)
        if not turn then
            break
        end

        table.insert(turns, turn)
    end

    return p0, p1, turns
end

local cell_formating = {
    EMPTY = {color = "default", char = "."},
    BEE_0 = {color = "blue", char = "B"},
    BEE_1 = {color = "red", char = "B"},
    BEE_0_WITH_FLOWER = {color = "blue", char = "f"},
    BEE_1_WITH_FLOWER = {color = "red", char = "f"},
    FLOWER = {color = "green", char = "F"},
    WALL = {color = "bgyellow", char = "W"},
    HIVE_0 = {color = "blue", char = "H", mod = "bold"},
    HIVE_1 = {color = "red", char = "H", mod = "bold"},
    OUTSIDE = {color = "default", char = "?"},
}

function print_cell(cell)

    local format = cell_formating[cell]

    term.color(term.colors[format.color], format.mod and term.colors[format.mod])
    io.write(' ', format.char, ' ')
    term.color(term.colors.reset)
end

function newline()
    term.down()
    term.left(9999)
end

function printnl(s)
    io.write(s)
    newline()
end

function print_message(message)

    term.golc(3 + constants.NUM_ROWS // 2, 5)
    term.color(term.colors.bold)
    io.write(message)
    term.color(term.colors.reset)

end

function print_turn(turns, current_turn, pname0, pname1)

    local turn = turns[current_turn]

    if turn.message then
        print_message(turn.message)
        return
    end

    term.color(term.colors.bold)
    io.write(("%s (%d) vs. %s (%d)"):format(pname0, turn.score0, pname1, turn.score1))
    term.color(term.colors.reset)
    newline()

    if turn.action then
        io.write(("Turn %d (player %d, bee %d)"):format(turn.turn, turn.player, turn.bee))
    else
        io.write(("Turn %d (game over)"):format(turn.turn))
    end
    newline()
    newline()

    for row = 0, constants.NUM_ROWS - 1 do
        for col = 0, constants.NUM_COLS - 1 do

            local index = row * constants.NUM_COLS + col + 1
            local cell = turn.cells[index]
            print_cell(cell)
        end
        newline()
    end

    newline()

    if turn.action then
        io.write(("Command: %s %s"):format(turn.action, turn.direction))
    end
end

function print_help()

    printnl("[←/→: previous/next turn, ↑/↓, previous/next round]")
    printnl("[A/D: start/end]")
    printnl("[space: play next ten rounds, enter: play whole match]")
    printnl("[esc/Q: quit]")
end

function clamp(x, min, max)
    if x > max then
        return max
    elseif x < min then
        return min
    else
        return x
    end
end

local function key(s)
    return string.byte(s)
end

function input_loop(turns, pname0, pname1)

    local current_turn = 1
    local play_mode = false
    local play_target = nil

    local get_input = term.input()

    local function move(delta)
        current_turn = clamp(current_turn + delta, 1, #turns)
    end

    local function play(delta)
        play_mode = true
        play_target = clamp(current_turn + delta, 1, #turns)
    end

    while true do

        term.clear()
        term.golc(0,0)

        print_turn(turns, current_turn, pname0, pname1)

        local help_row = 3 + constants.NUM_ROWS + 4
        term.golc(help_row, 0)

        if not play_mode then
            print_help()
        else
            io.write('[ ▶ ]')
        end

        io.flush()

        if not play_mode then
            local k = get_input()

            if k == term.keys.kright then
                move(1)
            elseif k == term.keys.kleft then
                move(-1)
            elseif k == term.keys.kup then
                move(10)
            elseif k == term.keys.kdown then
                move(-10)
            elseif k == key 'a' then
                current_turn = 1
            elseif k == key 'd' then
                current_turn = #turns
            elseif k == key ' ' then
                play(10 * 10)
            elseif k == 13 or k == 10 then
                play(#turns)
            elseif k == term.keys.esc or k == key 'q' then
                break
            end

        else
            if current_turn < play_target then
                move(10)
            elseif current_turn > play_target then
                move(-10)
            else
                play_mode = false
                play_target = nil
            end

            os.execute("sleep 0.08")
        end
    end
end


local term_mode

function set_terminal()
    term_mode = term.savemode()
    term.setrawmode()
    term.hide()
end

function reset_terminal()
    term.show()
    term.setsanemode()

    if term_mode then
        term.restoremode(term_mode)
    end
end

function panic(message)
    reset_terminal()
    error(message)
end

function run(args)
    local p0, p1, turns = load_log(args[1])

    if #turns > 0 then
        input_loop(turns, p0, p1)
    end
end

function main(args)

    set_terminal()

    if #args ~= 1 then
        panic("Usage: lua viewer.lua logfile")
    end

    local success, err = pcall(run, args)

    reset_terminal()

    if err then
        print(err)
    end
end

main({...})
