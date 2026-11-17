# A bot that joins under another name than its own, then exits
FROM busybox
ENTRYPOINT ["sh", "-c", "wget -qO- \"http://$ARENA_HOST/join?id=$ARENA_GAME_ID&name=impostor\"; sleep 1; exit 5"]
