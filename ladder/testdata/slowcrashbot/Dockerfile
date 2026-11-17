# A bot that joins late, then exits without playing
FROM busybox
ENTRYPOINT ["sh", "-c", "sleep 4; wget -qO- \"http://$ARENA_HOST/join?id=$ARENA_GAME_ID&name=$ARENA_NAME\"; exit 4"]
