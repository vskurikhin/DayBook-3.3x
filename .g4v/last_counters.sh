#!/bin/zsh
# last_counters.sh <leader-node>
# Печатает последнюю строку счётчиков из stdout узла-лидера.
# Строка счётчиков содержит "VerifyDone=" (уникальный маркер countersReport).
set -u
N="$1"
F="trace/raftkv-$N.stdout"
grep 'VerifyDone=' "$F" 2>/dev/null | tail -n 1
