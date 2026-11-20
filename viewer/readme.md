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

The view first fits the whole map in the window, and fits it again when the window is resized, until you zoom or click.

## In the browser

`./build-web.sh` builds the viewer to WebAssembly in `web/` (`index.html`, `viewer.wasm` and its compressed version, `wasm_exec.js`). Serve that directory and open `index.html?url=<URL of a game report>` (add `&autoplay=1` to start playing right away, and `&speed=<turns per second>`, 6 by default; `--autoplay` and `--speed` on the desktop). When an autoplayed replay reaches its last turn, the browser viewer sends a `{type: "hive-replay-ended"}` message to the page that embeds it. The ladder serves it at `/viewer/`, where `?match=<id>` opens the replay of one of its matches.

Watching live games (`--host`, `--id`, `--token`) is only available in the desktop version.

## License

Terrain tiles by [etahoshi](https://etahoshi.itch.io/), commercial license
