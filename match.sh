#!/usr/bin/env bash

set -e

if [ $# -lt 2 ] || [ $# -gt 3 ]; then
    echo "Usage: ./match agent0_directory agent1_directory [log_directory]"
    exit 1
fi

make -C src
make -C $1
make -C $2

team1=$(basename $1)
team2=$(basename $2)
isodate=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
logfile="${3:-.}/${team1}-${team2}-${isodate}.log"

port=8000

(sleep 2 ; cd $1 ; ./agent 127.0.0.1 $port | sed -e 's/^/[p0] /' ) &
(sleep 2 ; cd $2 ; ./agent 127.0.0.1 $port | sed -e 's/^/[p1] /' ) &

bin/arena $port $logfile
gzip $logfile

if [ -z $3 ]; then
    tools/viewer $logfile
fi
