Example Lua Agent
=================

## Requirements

- Lua 5.1 (or later)
- luasocket

On MacOS, the easiest way to install the requirements is as follows:

```
brew install lua luarocks
luarocks install luasocket
```

## How to use

The `libagent.lua` module mirrors closely the original C version, with the following adjustments for the language:

- structs become Lua tables
- C constants become strings (see file `tools/constants.lua`)
- In the `agent_info` table, the `cells` field uses indices relative to the bee's position. That is, the bee is at `[0][0]`, and indices range from `-VIEW_DISTANCE` to `VIEW_DISTANCE`
