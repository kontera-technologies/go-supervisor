#!/usr/bin/env bash

read greet

echo "$1 $greet"

>&2 echo "Bye $greet"

exit 5
