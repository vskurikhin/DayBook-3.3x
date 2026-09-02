#!/bin/zsh
# run_nodes.sh <raftkv-bin> <trace-level>
# Запускает 3 узла raftkv заданным бинарником на уровне трассировки trace-level.
# Очищает trace-файлы узлов перед запуском. Данные узлов предполагаются свежими.
# Вывод: PID каждого узла в /tmp/.raftkv-<n>.pid (внешний файл — для совместимости
# с окружением, где /tmp может быть недоступен на чтение; PID печатается в stdout).
set -u
RAFKTV_BIN="$1"
TRACE_LEVEL="$2"

# Очистка trace-файлов узлов и сводки фонового генератора.
rm -f trace/raftcm-1.trace trace/raftcm-2.trace trace/raftcm-3.trace
rm -f trace/raftkv-1.trace trace/raftkv-2.trace trace/raftkv-3.trace
rm -f trace/raftkv-1.stderr trace/raftkv-2.stderr trace/raftkv-3.stderr
rm -f trace/raftkv-1.stdout trace/raftkv-2.stdout trace/raftkv-3.stdout
rm -f trace/loadkv.out

for n in 1 2 3; do
  http="888$n"
  rpc="999$n"
  peers=""
  for p in 1 2 3; do
    if [ "$p" != "$n" ]; then
      peers="${peers}${p}=:999${p},"
    fi
  done
  peers="${peers%,}"
  "$RAFKTV_BIN" \
    -number "$n" \
    -http-addr=":$http" -rpc-addr=":$rpc" -peers="$peers" \
    -trace-log-level "$TRACE_LEVEL" \
    --trace-cm-log-file "trace/raftcm-$n.trace" \
    --trace-kv-log-file "trace/raftkv-$n.trace" \
    1>"trace/raftkv-$n.stdout" 2>"trace/raftkv-$n.stderr" &
  echo "node $n pid: $!"
done
