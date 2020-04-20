#!/usr/bin/env bash

./$1 & child_pid=$!

>&2 echo $child_pid

while :; do
    sleep 1
done