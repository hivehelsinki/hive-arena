FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY arena ./arena
COPY server ./server
COPY .git ./.git
# Embeds the git revision, printed at startup and in /status
RUN CGO_ENABLED=0 go build -C server -o /server .

FROM alpine:3.22
RUN apk add --no-cache tzdata
ENV TZ=Europe/Helsinki
WORKDIR /app
COPY --from=build /server server/server
EXPOSE 8080
CMD ["server/server", "-p", "8080"]
