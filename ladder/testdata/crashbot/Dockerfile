# A bot that joins its game, then exits without playing
FROM busybox
ENTRYPOINT ["sh", "-c", "wget -qO- \"http://$ARENA_HOST/join?id=$ARENA_GAME_ID&name=$ARENA_NAME\"; sleep 1; exit 3"]
