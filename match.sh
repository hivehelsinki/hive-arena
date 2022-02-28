#!/usr/bin/env bash

set -e

if [ $# -ne 2 ]; then
    echo "Usage: ./match agent0_directory agent1_directory"
    exit 1
fi

make -C src
make -C $1
make -C $2

team1=$(basename $1)
team2=$(basename $2)
isodate=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
logfile="${team1}-${team2}-${isodate}.log"

port=8000

(sleep 2 ; cd $1 ; ./agent 127.0.0.1 $port | sed -e 's/^/[p0] /' ) &
(sleep 2 ; cd $2 ; ./agent 127.0.0.1 $port | sed -e 's/^/[p1] /' ) &

bin/arena $port $logfile
gzip $logfile

tools/viewer $logfile
