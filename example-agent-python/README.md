Example Python Agent
====================

## Requirements

- Python 2 or 3 (tested with 2.7 and 3.9.9)

## How to use

The `libagent.py` module mirrors closely the original C version, with the following adjustments for the language:

- structs become Python dictionaries
- C constants become strings
- In the `agent_info` dictionary, the `cells` field is a dictionary of dictionaries, and uses indices relative to the bee's position. That is, the bee is at `[0][0]`, and indices range from `-VIEW_DISTANCE` to `VIEW_DISTANCE`
