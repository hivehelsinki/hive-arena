#!/bin/sh
# Builds the viewer for the browser, in viewer/web: open index.html?url=<report URL>
# from a web server (the ladder serves it at /viewer/).
set -e
cd "$(dirname "$0")"

GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o web/viewer.wasm .
gzip -9 -k -f web/viewer.wasm

root=$(go env GOROOT)
if [ -f "$root/lib/wasm/wasm_exec.js" ]; then
	cp "$root/lib/wasm/wasm_exec.js" web/
else
	cp "$root/misc/wasm/wasm_exec.js" web/
fi

echo "Built viewer/web"
