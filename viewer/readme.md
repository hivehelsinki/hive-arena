# Hive Arena Viewer

Display a completed game (such as the ones available on the `/history` route/directory of the server), or a game currently running.

Usage:

- `go run . --url <url>`
- `go run . --file <path>`
- `go run . --host <host> --id <game id> --token <token>`

Input:

- left/right: move to previous/next turn (hold shift to move 10 turns, ctrl or alt to move 50 turns)
- up/down: move to first/last turn
- space: play/pause
- w/q or mouse wheel: zoom in/out
- click: center view

## License

Terrain tiles by [etahoshi](https://etahoshi.itch.io/), commercial license
