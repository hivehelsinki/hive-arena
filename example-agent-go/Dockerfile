# Build from the repository root, as the agent uses the arena package:
#   docker build -f example-agent-go/Dockerfile -t example-agent-go .
# In your own repository, with your own go.mod, the build context is the
# repository root and the paths below change accordingly.

FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY arena ./arena
COPY example-agent-go ./example-agent-go
RUN CGO_ENABLED=0 go build -o /agent ./example-agent-go

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /agent /agent
# Reads ARENA_HOST, ARENA_GAME_ID and ARENA_NAME from the environment
ENTRYPOINT ["/agent"]
