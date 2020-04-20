#!/usr/bin/env bash

echo "STDOUT MESSAGE"
sleep 0.1
echo "STDERR MESSAGE" 1>&2
sleep 0.1