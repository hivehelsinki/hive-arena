# A bot that joins, then never plays nor exits: every turn waits for the timeout
FROM busybox
ENTRYPOINT ["sh", "-c", "wget -qO- \"http://$ARENA_HOST/join?id=$ARENA_GAME_ID&name=$ARENA_NAME\"; echo joined; sleep 3600"]
