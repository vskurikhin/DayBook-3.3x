#!/bin/zsh
# run_bench.sh <state> <level> <series> <n> -- <loadkv args...>
#   state  : base | target
#   level  : 0 | 1 (TRACE_LOG_LEVEL)
#   series : v1 | v2 | s2
#   n      : 1 | 2 | 3
# Единственный источник нагрузки: make start-raft поднимает 3 узла и фоновый
# loadkv (trace/loadkv.out, /tmp/.loadkv.pid) — фоновый генератор немедленно
# останавливается (pkill -x loadkv). Свежие data/node-* перед прогоном.
# Уровень трассировки — командной переменной make TRACE_LOG_LEVEL (правка
# Makefile запрещена). Сводка измерительного loadkv — в поимённый файл
# trace/bench-g4v-<series>-<state>-<n>.out; журналы узлов — data/g4v-logs/.
set -u

ROOT=/Volumes/t9-raid1/Users/svn/IdeaProjects/github.com/vskurikhin.localized/raft
cd "$ROOT" || exit 1

STATE="$1"; LEVEL="$2"; SERIES="$3"; N="$4"; shift 4
[ "$1" = "--" ] && shift

echo "== run: state=$STATE level=$LEVEL series=$SERIES n=$N loadkv=[$*] $(date +%H:%M:%S) =="

# 1. Гигиена: остановить любые узлы/генератор.
make stop-raft >/dev/null 2>&1
pkill -x loadkv 2>/dev/null

# 2. Выбрать бинарник состояния.
cp .g4v/bin-staging/raftkv-$STATE bin/raftkv

# 3. Свежие данные узлов.
make clean-data-files >/dev/null 2>&1

# 4. Старт узлов заданного уровня трассировки + фоновый генератор.
make start-raft TRACE_LOG_LEVEL=$LEVEL >/dev/null 2>&1

# 5. Единственный генератор: остановить фоновый loadkv.
sleep 1
pkill -x loadkv
sleep 1

# Проверка: pgrep -x loadkv пуст, pgrep -f raftkv == 3.
if [ -n "$(pgrep -x loadkv)" ]; then echo "WARN: loadkv still running"; fi
if [ "$(pgrep -f raftkv | wc -l | tr -d ' ')" != "3" ]; then echo "WARN: raftkv count != 3"; fi

# 6. Дождаться устойчивого лидера (по "wins election" в raftkv-<n>.stderr;
#    лидер пишет это в stderr даже при TRACE_LOG_LEVEL=0).
sleep 8
LEADER=""
for n in 1 2 3; do
  if grep -q "wins election" "trace/raftkv-$n.stderr" 2>/dev/null; then
    LEADER="$n"
  fi
done
echo "leader=$LEADER"

# 7. Измерительный loadkv (единственный источник), сводка в поимённый файл.
OUT="trace/bench-g4v-$SERIES-$STATE-$N.out"
TRACE_LOG_LEVEL=$LEVEL ./bin/loadkv "$@" > "$OUT" 2>&1
echo "summary: $OUT"

# 8. Последняя строка счётчиков узла-лидера (маркер VerifyDone).
echo "last-counter-leader=$LEADER:"
grep 'VerifyDone=' "trace/raftkv-$LEADER.stdout" 2>/dev/null | tail -n 1

# 9. Сохранить журналы узлов и сводку в data/g4v-logs/.
LOGDIR="data/g4v-logs/$SERIES-$STATE-$N"
mkdir -p "$LOGDIR"
cp "$OUT" "$LOGDIR/" 2>/dev/null
for n in 1 2 3; do
  cp "trace/raftkv-$n.stdout" "$LOGDIR/" 2>/dev/null
  cp "trace/raftkv-$n.stderr" "$LOGDIR/" 2>/dev/null
  cp "trace/raftkv-$n.trace" "$LOGDIR/" 2>/dev/null
  cp "trace/raftcm-$n.trace" "$LOGDIR/" 2>/dev/null
done

# 10. Остановка стенда.
make stop-raft >/dev/null 2>&1
pkill -x loadkv 2>/dev/null
echo "== done $(date +%H:%M:%S) =="
