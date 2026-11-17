# Build from this directory:
#   docker build -t example-agent-lua example-agent-lua

FROM alpine:3.22 AS build
RUN apk add --no-cache lua5.4 lua5.4-dev luarocks5.4 build-base openssl-dev bsd-compat-headers m4
RUN luarocks-5.4 install lunajson && luarocks-5.4 install http

FROM alpine:3.22
RUN apk add --no-cache lua5.4 libssl3 libcrypto3
COPY --from=build /usr/local /usr/local
WORKDIR /agent
COPY *.lua ./
USER nobody
# Reads ARENA_HOST, ARENA_GAME_ID and ARENA_NAME from the environment
ENTRYPOINT ["lua5.4", "main.lua"]
