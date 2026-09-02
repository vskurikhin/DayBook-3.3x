#!/bin/zsh
# leader.sh <level>
# Определяет узел-лидер.
# При уровне >= 1: лидер — узел, чей raftkv-<n>.stdout содержит строку
# счётчиков с префиксом [L,. При любом уровне: узел, чей raftkv-<n>.stderr
# содержит "wins election".
# Печатает номер узла-лидера.
set -u
LEVEL="$1"

# Способ 1: stderr "wins election".
for n in 1 2 3; do
  if grep -q "wins election" "trace/raftkv-$n.stderr" 2>/dev/null; then
    echo "stderr:$n"
  fi
done

# Способ 2 (уровень >= 1): счётчики лидера в stdout.
if [ "$LEVEL" -ge 1 ]; then
  for n in 1 2 3; do
    if grep -q '^\[L,' "trace/raftkv-$n.stdout" 2>/dev/null; then
      echo "stdout:$n"
    fi
  done
fi
