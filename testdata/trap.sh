#!/usr/bin/env bash

trap '' TERM INT

./$1 & child_pid=$!

>&2 echo $child_pid

while :; do
    sleep 0.01
done