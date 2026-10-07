package raft

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// AST-гейт guards: постоянная проверка производственных мест трассировки
// консенсус-модуля и ссылочных аргументов. Проверяются:
//   - 48 вызовов traceLogfLocked, 5 вызовов traceSprintfLocked и 22
//     вызова traceLogf (всего 75 мест) обёрнуты положительной ветвью
//     if traceEnabled(L) с единственным оператором; уровень и формат
//     каждого места независимо сверяются с эталонным реестром;
//   - тело (*ConsensusModule).traceLogf безусловно захватывает cm.mu,
//     сразу откладывает снятие и содержит единственный прямой вызов
//     enqueueTraceLocked без условных операторов;
//   - тела traceLogfLocked/traceSprintfLocked содержат один прямой
//     вызов enqueue-метода без условных операторов;
//   - прямые вызовы enqueueTrace*Locked в производственных файлах
//     встречаются только в этих трёх обёртках;
//   - асинхронные вызовы (traceLogfLocked, traceLogf) не читают
//     ссылочные аргументы: ни срез, ни карту, ни указатель; ссылочные
//     тела проходят только через traceSprintfLocked;
//   - ровно пять ссылочных мест совпадают с реестром (файл, функция,
//     уровень, формат, выражения и типы аргументов);
//   - failedAETrace вычисляется только внутри guard ступени репликации;
//   - пять KV-мест elapsed обёрнуты if _traceKV > 0, остальные шесть
//     вызовов KV не обёрнуты.
//
// Отрицательные примеры мутируют разобранное дерево в памяти и обязаны
// быть отвергнуты: изменение уровня guard, перенос вызова в else, новая
// ссылка в асинхронном вызове, понижение traceSprintfLocked, потеря
// безусловной блокировки или отложенного снятия в теле traceLogf,
// возврат сравнения уровня в тело, возврат старой сигнатуры с первым
// аргументом-уровнем, подмена допустимого уровня guard и прикладной
// прямой вызов enqueue-метода.

const (
	// guardWantLocked — число прикладных вызовов traceLogfLocked.
	guardWantLocked = 48
	// guardWantSprintf — число вызовов traceSprintfLocked (ссылочные места).
	guardWantSprintf = 5
	// guardWantPlain — число вызовов traceLogf (обёртка, берущая cm.mu сама).
	guardWantPlain = 22
	// guardWantTotal — все прикладные места (48 + 5 + 22); тела трёх
	// обёрток проверяются отдельными правилами, не этим счётчиком.
	guardWantTotal = guardWantLocked + guardWantSprintf + guardWantPlain
)

// guardArg — аргумент вызова трассировки: исходное выражение и его тип.
type guardArg struct {
	expr string
	typ  string
}

// guardPlace — одно место вызова трассировочного помощника. Уровень
// места берётся из положительной ветви if traceEnabled(L): параметра
// уровня в сигнатурах методов нет.
type guardPlace struct {
	file       string
	owner      string
	callee     string
	guardLevel string
	format     string
	args       []guardArg
	wrapped    bool
	sole       bool
	guardKind  string

	call      *ast.CallExpr
	ifStmt    *ast.IfStmt
	guardCall *ast.CallExpr
}

// position возвращает краткое описание места для сообщения об ошибке.
func (p *guardPlace) position() string {
	return fmt.Sprintf("%s %s %s", p.file, p.owner, p.callee)
}

// guardRefPlace — эталонная запись реестра пяти ссылочных мест.
type guardRefPlace struct {
	file   string
	owner  string
	level  string
	format string
	args   []guardArg
}

// guardRefPlaces — реестр пяти мест со ссылочными аргументами. Уровни и
// форматы совпадают с производственным кодом; изменения мест обязаны
// сознательно обновлять реестр.
var guardRefPlaces = []guardRefPlace{
	{
		file:   "raft_cm_leader.go",
		owner:  "(*ConsensusModule).startLeaderLocked",
		level:  "_traceLevelKeyEvents",
		format: "becomes Leader; term=%d, nextIndex=%v, matchIndex=%v; len(log)=%d",
		args: []guardArg{
			{expr: "cm.cmState.currentTerm", typ: "int"},
			{expr: "cm.leaderState.nextIndex", typ: "map[int]int"},
			{expr: "cm.leaderState.matchIndex", typ: "map[int]int"},
			{expr: "len(cm.cmState.log)", typ: "int"},
		},
	},
	{
		file:   "raft_cm_replication.go",
		owner:  "(*ConsensusModule).applyAESuccessLocked",
		level:  "_traceLevelReplication",
		format: "AppendEntries reply from %d success: nextIndex := %v, matchIndex := %v; commitIndex := %d",
		args: []guardArg{
			{expr: "peerID", typ: "int"},
			{expr: "cm.leaderState.nextIndex", typ: "map[int]int"},
			{expr: "cm.leaderState.matchIndex", typ: "map[int]int"},
			{expr: "cm.leaderState.commitmentTracker.getCommitIndex()", typ: "int"},
		},
	},
	{
		file:   "raft_cm_rpc.go",
		owner:  "(*ConsensusModule).appendMatchingEntriesLocked",
		level:  "_traceLevelReplication",
		format: "... inserting entries %v from index %d",
		args: []guardArg{
			{expr: "args.Entries[newEntriesIndex:]", typ: "[]contract.LogEntry"},
			{expr: "logInsertIndex", typ: "int"},
		},
	},
	{
		file:   "raft_cm_rpc.go",
		owner:  "(*ConsensusModule).appendMatchingEntriesLocked",
		level:  "_traceLevelLogDump",
		format: "... log is now: %v",
		args: []guardArg{
			{expr: "cm.cmState.log", typ: "[]raft.LogEntry"},
		},
	},
	{
		file:   "raft_cm_rpc.go",
		owner:  "(*ConsensusModule).RequestVote",
		level:  "_traceLevelPreVote",
		format: "... RequestVote reply: %+v",
		args: []guardArg{
			{expr: "reply", typ: "*raft.RequestVoteReply"},
		},
	},
}

// guardBaselinePlace — запись эталонного реестра производственных мест:
// файл, функция-владелец, метод, уровень сообщения и формат.
type guardBaselinePlace struct {
	file   string
	owner  string
	callee string
	level  string
	format string
}

// guardBaselinePlaces — эталонный реестр уровней и форматов 75 прикладных
// мест. Составлен по снимку трассировки до введения охранных проверок
// (исходная ревизия d9f83d6) и перенесён в тест для независимой сверки:
// уровни и форматы не снимаются с текущего дерева. Внутренняя делегация
// в счёт не входит. При переносе выполнены два осознанных преобразования:
// склеенные строковые литералы записаны одним форматом, а пять ссылочных
// мест отнесены к traceSprintfLocked (переименование после исходного
// снимка). Изменение места, уровня, формата или метода обязано
// сознательно обновлять реестр.
var guardBaselinePlaces = []guardBaselinePlace{
	// raft_cm.go
	{"raft_cm.go", "(*ConsensusModule).Stop", "traceLogf", "_traceLevelKeyEvents", "CM.Stop called / becomes Dead"},

	// raft_cm_apply.go
	{"raft_cm_apply.go", "(*ConsensusModule).sendBatch", "traceLogfLocked", "_traceLevelKeyEvents", "sendBatch: logPositionLocked(%d) returned %d, len(log)=%d"},
	{"raft_cm_apply.go", "(*ConsensusModule).sendBatch", "traceLogfLocked", "_traceLevelKeyEvents", "sendBatch: no log entry at index %d (entry=%d), skipping"},

	// raft_cm_election.go
	{"raft_cm_election.go", "(*ConsensusModule).becomeFollowerLocked", "traceLogfLocked", "_traceLevelKeyEvents", "becomes Follower with term=%d; len(log)=%v"},
	{"raft_cm_election.go", "(*ConsensusModule).startElectionLocked", "traceLogfLocked", "_traceLevelKeyEvents", "becomes Candidate (currentTerm=%d); len(log)=%v"},
	{"raft_cm_election.go", "(*ConsensusModule).requestVoteFromPeer", "traceLogf", "_traceLevelKeyEvents", "sending RequestVote to %d: %+v"},
	{"raft_cm_election.go", "(*ConsensusModule).requestVoteFromPeer", "traceLogfLocked", "_traceLevelKeyEvents", "received RequestVoteReply %+v"},
	{"raft_cm_election.go", "(*ConsensusModule).requestVoteFromPeer", "traceLogfLocked", "_traceLevelKeyEvents", "while waiting for reply, state = %v"},
	{"raft_cm_election.go", "(*ConsensusModule).requestVoteFromPeer", "traceLogfLocked", "_traceLevelKeyEvents", "term out of date in RequestVoteReply"},
	{"raft_cm_election.go", "(*ConsensusModule).requestVoteFromPeer", "traceLogfLocked", "_traceLevelProgress", "wins election with %d votes"},
	{"raft_cm_election.go", "(*ConsensusModule).runElectionTimer", "traceLogf", "_traceLevelLoops", "election timer started (%v), term=%d"},
	{"raft_cm_election.go", "(*ConsensusModule).runElectionTimer", "traceLogfLocked", "_traceLevelLoops", "in election timer state=%s, bailing out"},
	{"raft_cm_election.go", "(*ConsensusModule).runElectionTimer", "traceLogfLocked", "_traceLevelLoops", "in election timer term changed from %d to %d, bailing out"},
	{"raft_cm_election.go", "(*ConsensusModule).runPreCandidate", "traceLogfLocked", "_traceLevelKeyEvents", "becomes PreCandidate; term=%d, len(log)=%d"},
	{"raft_cm_election.go", "(*ConsensusModule).sendPreVoteToPeer", "traceLogf", "_traceLevelKeyEvents", "runPreCandidate: transport is nil, cannot send to %d"},
	{"raft_cm_election.go", "(*ConsensusModule).sendPreVoteToPeer", "traceLogf", "_traceLevelPreVote", "sending RequestPreVote to %d: %+v"},
	{"raft_cm_election.go", "(*ConsensusModule).sendPreVoteToPeer", "traceLogf", "_traceLevelPreVote", "RequestPreVote to %d failed: %v"},
	{"raft_cm_election.go", "(*ConsensusModule).collectPreVoteReplies", "traceLogfLocked", "_traceLevelPreVote", "runPreCandidate: state changed to %s, bailing out"},
	{"raft_cm_election.go", "(*ConsensusModule).collectPreVoteReplies", "traceLogfLocked", "_traceLevelPreVote", "runPreCandidate: found higher term %d"},
	{"raft_cm_election.go", "(*ConsensusModule).collectPreVoteReplies", "traceLogf", "_traceLevelPreVote", "runPreCandidate: granted vote from peer, total=%d, needed=%d"},
	{"raft_cm_election.go", "(*ConsensusModule).collectPreVoteReplies", "traceLogfLocked", "_traceLevelPreVote", "runPreCandidate: pre-vote timeout, returning to follower"},
	{"raft_cm_election.go", "(*ConsensusModule).collectPreVoteReplies", "traceLogfLocked", "_traceLevelPreVote", "runPreCandidate: pre-vote lost (%d/%d), returning to follower"},
	{"raft_cm_election.go", "(*ConsensusModule).startElectionAfterPreVote", "traceLogfLocked", "_traceLevelPreVote", "runPreCandidate: won pre-vote with %d votes, starting election"},
	{"raft_cm_election.go", "(*ConsensusModule).timeoutNow", "traceLogf", "_traceLevelKeyEvents", "received TimeoutNow from %d"},
	{"raft_cm_election.go", "(*ConsensusModule).timeoutNow", "traceLogfLocked", "_traceLevelLoops", "candidateFromLeadershipTransfer set to true (cm.id=%d)"},
	{"raft_cm_election.go", "(*ConsensusModule).timeoutNow", "traceLogfLocked", "_traceLevelLoops", "started immediate election after TimeoutNow, term=%d (cm.id=%d)"},

	// raft_cm_leader.go
	{"raft_cm_leader.go", "(*ConsensusModule).appendConfigurationEntry", "traceLogfLocked", "_traceLevelProgress", "leader sets commitIndex := %d"},
	{"raft_cm_leader.go", "(*ConsensusModule).runLeaderLoop", "traceLogf", "_traceLevelLoops", "leaderLoop exit: elapsed=%v"},
	{"raft_cm_leader.go", "(*ConsensusModule).runLeaderLoop", "traceLogfLocked", "_traceLevelLoops", "leader stepping down"},
	{"raft_cm_leader.go", "(*ConsensusModule).leaderLoopExitCleanupLocked", "traceLogfLocked", "_traceLevelLoops", "leaderLoop exit: responding to %d inflight futures"},
	{"raft_cm_leader.go", "(*ConsensusModule).handleLeaderCommitAdvance", "traceLogfLocked", "_traceLevelProgress", "leader sets commitIndex := %d"},
	{"raft_cm_leader.go", "(*ConsensusModule).handleLeaderCommitAdvance", "traceLogfLocked", "_traceLevelLoops", "leader stepping down: not in committed configuration"},
	{"raft_cm_leader.go", "(*ConsensusModule).checkQuorumContact", "traceLogfLocked", "_traceLevelKeyEvents", "leader stepping down: no contact with quorum of voters for %v"},
	{"raft_cm_leader.go", "(*ConsensusModule).startLeaderLocked", "traceSprintfLocked", "_traceLevelKeyEvents", "becomes Leader; term=%d, nextIndex=%v, matchIndex=%v; len(log)=%d"},

	// raft_cm_log.go
	{"raft_cm_log.go", "(*ConsensusModule).dispatchLogs", "traceLogfLocked", "_traceLevelProgress", "leader sets commitIndex := %d"},

	// raft_cm_replication.go
	{"raft_cm_replication.go", "(*ConsensusModule).nextIndexArgsEntries", "traceLogfLocked", "_traceLevelLoops", "nextIndexArgsEntries: logPositionLocked(%d) out of range (len=%d)"},
	{"raft_cm_replication.go", "(*ConsensusModule).leaderSendAEsToPeer", "traceLogf", "_traceLevelReplication", "sending AppendEntries to %v: ni=%d, args=%+v"},
	{"raft_cm_replication.go", "(*ConsensusModule).handleAEReply", "traceLogfLocked", "_traceLevelReplication", "term out of date in heartbeat reply"},
	{"raft_cm_replication.go", "(*ConsensusModule).applyAESuccessLocked", "traceSprintfLocked", "_traceLevelReplication", "AppendEntries reply from %d success: nextIndex := %v, matchIndex := %v; commitIndex := %d"},
	{"raft_cm_replication.go", "(*ConsensusModule).handleFailedAEReplyLocked", "traceLogfLocked", "_traceLevelKeyEvents", "AppendEntries reply from %d rejected (impossible): peer=%d term=%d ni=%d matchIndex=%d ConflictIndex=%d ConflictTerm=%d"},
	{"raft_cm_replication.go", "(*ConsensusModule).handleFailedAEReplyLocked", "traceLogfLocked", "_traceLevelReplication", "%s"},
	{"raft_cm_replication.go", "(*ConsensusModule).leaderSendSnapshot", "traceLogf", "_traceLevelPreVote", "leaderSendSnapshot elapsed %s"},
	{"raft_cm_replication.go", "(*ConsensusModule).leaderSendSnapshot", "traceLogf", "_traceLevelPreVote", "leaderSendSnapshot peer %d: term=%d"},
	{"raft_cm_replication.go", "(*ConsensusModule).leaderSendSnapshot", "traceLogf", "_traceLevelPreVote", "leaderSendSnapshot: snapshotStore is nil, cannot send to %d"},
	{"raft_cm_replication.go", "(*ConsensusModule).leaderSendSnapshot", "traceLogf", "_traceLevelKeyEvents", "leaderSendSnapshot: cannot send snapshot to %d: err=%v, snapshots=%d"},
	{"raft_cm_replication.go", "(*ConsensusModule).leaderSendSnapshot", "traceLogf", "_traceLevelPreVote", "leaderSendSnapshot: snapshots[0] is nil"},
	{"raft_cm_replication.go", "(*ConsensusModule).leaderSendSnapshot", "traceLogf", "_traceLevelPreVote", "leaderSendSnapshot: snapshots[0].ID is empty"},
	{"raft_cm_replication.go", "(*ConsensusModule).leaderSendSnapshot", "traceLogf", "_traceLevelKeyEvents", "leaderSendSnapshot: cannot open snapshot %s for peer %d: %v"},

	// raft_cm_rpc.go
	{"raft_cm_rpc.go", "(*ConsensusModule).handleRPC", "traceLogf", "_traceLevelReplication", "handleRPC: %T"},
	{"raft_cm_rpc.go", "(*ConsensusModule).respondRPC", "traceLogf", "_traceLevelLoops", "rpc.RespChan closed/unavailable for %T"},
	{"raft_cm_rpc.go", "(*ConsensusModule).AppendEntries", "traceLogfLocked", "_traceLevelReplication", "AppendEntries: %v, Term:%d LeaderID:%d PrevLogIndex:%d PrevLogTerm:%d len(Entries):%d"},
	{"raft_cm_rpc.go", "(*ConsensusModule).AppendEntries", "traceLogfLocked", "_traceLevelReplication", "... term out of date in AppendEntries"},
	{"raft_cm_rpc.go", "(*ConsensusModule).AppendEntries", "traceLogfLocked", "_traceLevelReplication", "AppendEntries reply: %+v"},
	{"raft_cm_rpc.go", "(*ConsensusModule).appendEntriesDuringLeadershipTransferLocked", "traceLogfLocked", "_traceLevelReplication", "... term out of date in AppendEntries (candidate from LT)"},
	{"raft_cm_rpc.go", "(*ConsensusModule).appendMatchingEntriesLocked", "traceSprintfLocked", "_traceLevelReplication", "... inserting entries %v from index %d"},
	{"raft_cm_rpc.go", "(*ConsensusModule).appendMatchingEntriesLocked", "traceSprintfLocked", "_traceLevelLogDump", "... log is now: %v"},
	{"raft_cm_rpc.go", "(*ConsensusModule).appendMatchingEntriesLocked", "traceLogfLocked", "_traceLevelReplication", "... setting commitIndex=%d"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestVote", "traceLogfLocked", "_traceLevelPreVote", "... RequestVote denied: not a voter"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestVote", "traceLogfLocked", "_traceLevelPreVote", "RequestVote: %+v [currentTerm=%d, votedFor=%d, log index/term=(%d, %d)]"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestVote", "traceLogfLocked", "_traceLevelPreVote", "... term out of date in RequestVote"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestVote", "traceLogfLocked", "_traceLevelPreVote", "... leader known, denying vote for %d"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestVote", "traceLogfLocked", "_traceLevelPreVote", "... vote denied: termOk=%v (cur=%d, args=%d), voteOk=%v (votedFor=%d, cand=%d), logOk=%v (args=(%d,%d), local=(%d,%d))"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestVote", "traceSprintfLocked", "_traceLevelPreVote", "... RequestVote reply: %+v"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestPreVote", "traceLogfLocked", "_traceLevelPreVote", "RequestPreVote: %+v [currentTerm=%d, log index/term=(%d, %d)]"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestPreVote", "traceLogfLocked", "_traceLevelPreVote", "... RequestPreVote denied: not a voter"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestPreVote", "traceLogfLocked", "_traceLevelPreVote", "... RequestPreVote denied: older term"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestPreVote", "traceLogfLocked", "_traceLevelPreVote", "... RequestPreVote denied: leader known"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestPreVote", "traceLogfLocked", "_traceLevelPreVote", "... RequestPreVote denied: log is more up-to-date"},
	{"raft_cm_rpc.go", "(*ConsensusModule).RequestPreVote", "traceLogfLocked", "_traceLevelPreVote", "... RequestPreVote granted"},

	// raft_cm_snapshot.go
	{"raft_cm_snapshot.go", "(*ConsensusModule).handleFsmSnapshot", "traceLogfLocked", "_traceLevelPreVote", "handleFsmSnapshot: FSM behind dispatch: fsmAppliedIndex=%d lastApplied=%d lastSnapshotIndex=%d"},
	{"raft_cm_snapshot.go", "(*ConsensusModule).handleInstallSnapshot", "traceLogf", "_traceLevelPreVote", "InstallSnapshot skipped as no-op: LastLogIndex=%d <= lastSnapshotIndex=%d (leaderID=%d)"},
	{"raft_cm_snapshot.go", "(*ConsensusModule).installSnapshotStateLocked", "traceLogfLocked", "_traceLevelKeyEvents", "handleInstallSnapshot: snapshot/log boundary violation: %v (lastLogIndex=%d, lastSnapshotIndex=%d)"},
	{"raft_cm_snapshot.go", "(*ConsensusModule).runSnapshots", "traceLogf", "_traceLevelKeyEvents", "runSnapshots: takeSnapshot failed: %v"},
	{"raft_cm_snapshot.go", "(*ConsensusModule).runSnapshots", "traceLogf", "_traceLevelKeyEvents", "runSnapshots: takeSnapshot failed: %v"},

	// raft_cm_storage.go
	{"raft_cm_storage.go", "(*ConsensusModule).persistToStorage", "traceLogfLocked", "_traceLevelProgress", "persistToStorage elapsed %s"},
}

// kvGuardFormats — пять KV-мест elapsed, единственные подлежащие обёртке
// if _traceKV > 0; остальные вызовы KV не оборачиваются.
var kvGuardFormats = []string{
	"HTTP WEAK-GET %v took %v",
	"HTTP CAS %v took %v",
	"HTTP DELETE %v took %v",
	"HTTP GET %v took %v",
	"HTTP PUT %v took %v",
}

// guardListedPackage — пакет в выводе go list -json.
type guardListedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
}

var (
	guardRootOnce  sync.Once
	guardRootDir   string
	guardRootFiles []string
	guardExportMap map[string]string
	guardRootErr   error
)

// guardRootPackage снимает у go list каталог корневого пакета, список
// production-файлов и карту export data зависимостей. Выполняется один
// раз на процесс: при -count повторные прогоны переиспользуют снимок.
func guardRootPackage() (string, []string, map[string]string, error) {
	guardRootOnce.Do(func() {
		dir, err := filepath.Abs(".")
		if err != nil {
			guardRootErr = err
			return
		}
		cmd := exec.Command("go", "list", "-export", "-deps", "-json", ".")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			guardRootErr = fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
			return
		}
		export := map[string]string{}
		dec := json.NewDecoder(bytes.NewReader(out))
		for {
			var pkg guardListedPackage
			if err := dec.Decode(&pkg); err == io.EOF {
				break
			} else if err != nil {
				guardRootErr = fmt.Errorf("разбор go list: %w", err)
				return
			}
			if pkg.Export != "" {
				export[pkg.ImportPath] = pkg.Export
			}
			if pkg.Dir == dir {
				guardRootDir = dir
				guardRootFiles = slices.Clone(pkg.GoFiles)
			}
		}
		if guardRootDir == "" || len(guardRootFiles) == 0 {
			guardRootErr = errors.New("go list не вернул корневой пакет")
			return
		}
		sort.Strings(guardRootFiles)
		guardExportMap = export
	})
	return guardRootDir, guardRootFiles, guardExportMap, guardRootErr
}

// guardPackage — разобранный и типизированный корневой пакет.
type guardPackage struct {
	fset    *token.FileSet
	files   []*ast.File
	info    *types.Info
	parents map[ast.Node]ast.Node
}

// parseGuardPackage разбирает production-файлы корневого пакета и
// типизирует их по export data зависимостей: тип аргумента — основа
// запрета ссылок в асинхронном пути.
func parseGuardPackage(t *testing.T) *guardPackage {
	t.Helper()
	dir, goFiles, export, err := guardRootPackage()
	if err != nil {
		t.Fatalf("подготовка AST-гейта: %v", err)
	}
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(goFiles))
	for _, name := range goFiles {
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("разбор %s: %v", name, err)
		}
		files = append(files, file)
	}
	lookup := func(path string) (io.ReadCloser, error) {
		if path == "unsafe" {
			return nil, errors.New("для unsafe нет export data")
		}
		ep, ok := export[path]
		if !ok || ep == "" {
			return nil, fmt.Errorf("нет export data для %q", path)
		}
		return os.Open(ep)
	}
	var typeErrs []string
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "gc", lookup),
		Error:    func(err error) { typeErrs = append(typeErrs, err.Error()) },
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	if _, err := conf.Check("github.com/vskurikhin/raft", fset, files, info); err != nil {
		typeErrs = append(typeErrs, err.Error())
	}
	if len(typeErrs) > 0 {
		t.Fatalf("типизация корневого пакета: %v", typeErrs)
	}
	return &guardPackage{fset: fset, files: files, info: info, parents: guardParentsFor(files)}
}

// guardParentsFor строит карту «узел → родитель» для набора файлов.
func guardParentsFor(files []*ast.File) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			if len(stack) > 0 {
				parents[n] = stack[len(stack)-1]
			}
			stack = append(stack, n)
			return true
		})
	}
	return parents
}

// printGuardExpr печатает выражение одной строкой без привязки к разметке.
func printGuardExpr(fset *token.FileSet, expr ast.Expr) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, expr); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// guardReceiverNamed разбирает тип получателя вызова до «имя пакета,
// имя типа»: позволяет отсечь одноимённые вызовы других типов.
func guardReceiverNamed(t types.Type) (string, string) {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		obj := named.Obj()
		if obj.Pkg() != nil {
			return obj.Pkg().Name(), obj.Name()
		}
		return "", obj.Name()
	}
	return "", ""
}

// guardOwnerName возвращает полное имя функции-владельца места.
func guardOwnerName(pkg *guardPackage, n ast.Node) string {
	for parent := pkg.parents[n]; parent != nil; parent = pkg.parents[parent] {
		fn, ok := parent.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			return "(" + printGuardExpr(pkg.fset, fn.Recv.List[0].Type) + ")." + fn.Name.Name
		}
		return fn.Name.Name
	}
	return "?"
}

// guardGuardKindLevel разбирает условие обёртки.
func guardGuardKindLevel(fset *token.FileSet, cond ast.Expr) (kind, level string) {
	switch x := cond.(type) {
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		if ok && id.Name == "traceEnabled" && len(x.Args) == 1 {
			return "traceEnabled", printGuardExpr(fset, x.Args[0])
		}
	case *ast.BinaryExpr:
		if x.Op == token.LSS {
			if id, ok := x.Y.(*ast.Ident); ok && id.Name == "_traceCM" {
				return "_traceCM", printGuardExpr(fset, x.X)
			}
		}
	}
	return "none", ""
}

// guardWrapInfo сообщает, обёрнут ли вызов положительной ветвью if, и
// является ли он единственным оператором тела.
func (pkg *guardPackage) guardWrapInfo(call *ast.CallExpr) (wrapped, sole bool, kind, level string, ifStmt *ast.IfStmt) {
	stmt, ok := pkg.parents[call].(*ast.ExprStmt)
	if !ok {
		return false, false, "none", "", nil
	}
	block, ok := pkg.parents[stmt].(*ast.BlockStmt)
	if !ok {
		return false, false, "none", "", nil
	}
	parent, ok := pkg.parents[block].(*ast.IfStmt)
	if !ok {
		return false, false, "none", "", nil
	}
	sole = len(block.List) == 1
	ifStmt = parent
	if parent.Body != block {
		return false, sole, "none", "", ifStmt
	}
	kind, level = guardGuardKindLevel(pkg.fset, parent.Cond)
	return kind != "none", sole, kind, level, ifStmt
}

// collectGuardPlaces собирает все вызовы traceLogfLocked,
// traceSprintfLocked и traceLogf у получателя *ConsensusModule.
func collectGuardPlaces(pkg *guardPackage) []*guardPlace {
	var places []*guardPlace
	for _, file := range pkg.files {
		base := filepath.Base(pkg.fset.Position(file.Pos()).Filename)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if name != "traceLogfLocked" && name != "traceSprintfLocked" && name != "traceLogf" {
				return true
			}
			pkgName, recv := guardReceiverNamed(pkg.info.Types[sel.X].Type)
			if pkgName != "raft" || recv != "ConsensusModule" {
				return true
			}
			place := &guardPlace{
				file:   base,
				owner:  guardOwnerName(pkg, call),
				callee: name,
				call:   call,
			}
			const formatIdx = 0
			if len(call.Args) > formatIdx {
				place.format = guardFormatArg(call.Args[formatIdx])
				for _, arg := range call.Args[formatIdx+1:] {
					place.args = append(place.args, guardArg{
						expr: printGuardExpr(pkg.fset, arg),
						typ:  guardArgType(pkg.info, arg),
					})
				}
			}
			place.wrapped, place.sole, place.guardKind, place.guardLevel, place.ifStmt = pkg.guardWrapInfo(call)
			if place.ifStmt != nil {
				if guardCall, ok := place.ifStmt.Cond.(*ast.CallExpr); ok && len(guardCall.Args) == 1 {
					place.guardCall = guardCall
				}
			}
			places = append(places, place)
			return true
		})
	}
	return places
}

// guardFormatArg извлекает строку формата из аргумента: строковые
// литералы и их конкатенация склеиваются; иное выражение помечается
// «?» и проваливает сверку с эталонным реестром.
func guardFormatArg(expr ast.Expr) string {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if value, err := strconv.Unquote(lit.Value); err == nil {
			return value
		}
		return "?"
	}
	if binary, ok := expr.(*ast.BinaryExpr); ok && binary.Op == token.ADD {
		left, right := guardFormatArg(binary.X), guardFormatArg(binary.Y)
		if left != "?" && right != "?" {
			return left + right
		}
	}
	return "?"
}

// guardArgType возвращает строковый вид типа аргумента; отсутствие типа
// помечается «?» и проваливает проверку.
func guardArgType(info *types.Info, expr ast.Expr) string {
	typ := info.Types[expr].Type
	if typ == nil {
		return "?"
	}
	return types.TypeString(typ, func(pkg *types.Package) string {
		if pkg == nil {
			return "?"
		}
		return pkg.Name()
	})
}

// guardReferenceType сообщает, является ли тип ссылочным для целей
// асинхронного пути: срез, указатель, карта, канал или функция.
func guardReferenceType(typ string) bool {
	return strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "*") ||
		strings.HasPrefix(typ, "map[") || strings.HasPrefix(typ, "chan ") ||
		strings.HasPrefix(typ, "func(")
}

// checkGuardPlaces проверяет структурные инварианты мест и возвращает
// список нарушений.
func checkGuardPlaces(places []*guardPlace) []string {
	var errs []string
	locked, sprintf, plain := 0, 0, 0
	for _, place := range places {
		if !strings.HasPrefix(place.file, "raft_cm") {
			errs = append(errs, fmt.Sprintf("%s: вызов вне карты файлов raft_cm*.go", place.position()))
		}
		switch place.callee {
		case "traceLogfLocked":
			locked++
		case "traceSprintfLocked":
			sprintf++
		case "traceLogf":
			plain++
		}
	}
	if len(places) != guardWantTotal {
		errs = append(errs, fmt.Sprintf("всего мест %d, ожидалось %d (75 прикладных мест)", len(places), guardWantTotal))
	}
	if locked != guardWantLocked {
		errs = append(errs, fmt.Sprintf("traceLogfLocked: %d мест, ожидалось %d", locked, guardWantLocked))
	}
	if sprintf != guardWantSprintf {
		errs = append(errs, fmt.Sprintf("traceSprintfLocked: %d мест, ожидалось %d", sprintf, guardWantSprintf))
	}
	if plain != guardWantPlain {
		errs = append(errs, fmt.Sprintf("traceLogf: %d мест, ожидалось %d", plain, guardWantPlain))
	}

	for _, place := range places {
		if !place.wrapped || !place.sole || place.guardKind != "traceEnabled" {
			errs = append(errs, fmt.Sprintf(
				"%s: нет обёртки if traceEnabled(L) с единственным оператором "+
					"(wrapped=%v sole=%v kind=%q guard=%q)",
				place.position(), place.wrapped, place.sole, place.guardKind, place.guardLevel))
		}
		if place.callee == "traceSprintfLocked" {
			continue
		}
		for _, arg := range place.args {
			switch {
			case arg.typ == "?":
				errs = append(errs, fmt.Sprintf("%s: нет типа аргумента %q", place.position(), arg.expr))
			case guardReferenceType(arg.typ):
				errs = append(errs, fmt.Sprintf(
					"%s: асинхронный вызов читает ссылочный аргумент %q типа %s (нужен traceSprintfLocked)",
					place.position(), arg.expr, arg.typ))
			}
		}
	}

	matched := make([]bool, len(guardRefPlaces))
	for _, place := range places {
		if place.callee != "traceSprintfLocked" {
			continue
		}
		known := false
		for i := range guardRefPlaces {
			if guardRefPlaceMatches(place, &guardRefPlaces[i]) {
				matched[i] = true
				known = true
				break
			}
		}
		if !known {
			errs = append(errs, fmt.Sprintf(
				"%s: место traceSprintfLocked отсутствует в реестре пяти ссылочных мест", place.position()))
		}
	}
	for i, ref := range guardRefPlaces {
		if !matched[i] {
			errs = append(errs, fmt.Sprintf(
				"реестр ссылочных мест: не найдено %s %s %q уровня %s",
				ref.file, ref.owner, ref.format, ref.level))
		}
	}
	errs = append(errs, guardBaselineErrors(places)...)
	return errs
}

// guardBaselineErrors независимо сверяет каждое прикладное место и его
// формат с эталонным реестром: совпадение ищется по файлу, функции,
// методу, уровню и формату среди ещё не сопоставленных записей, поэтому
// несколько сообщений одной функции не схлопываются. Не найденное место
// и не сопоставленная запись реестра одинаково считаются нарушением.
func guardBaselineErrors(places []*guardPlace) []string {
	var errs []string
	matched := make([]bool, len(guardBaselinePlaces))
	for _, place := range places {
		found := false
		for i := range guardBaselinePlaces {
			if matched[i] {
				continue
			}
			ref := &guardBaselinePlaces[i]
			if place.file == ref.file && place.owner == ref.owner &&
				place.callee == ref.callee && place.guardLevel == ref.level &&
				place.format == ref.format {
				matched[i] = true
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, fmt.Sprintf(
				"%s: место отсутствует в эталонном реестре уровней и форматов (уровень %q, формат %q)",
				place.position(), place.guardLevel, place.format))
		}
	}
	for i, ref := range guardBaselinePlaces {
		if !matched[i] {
			errs = append(errs, fmt.Sprintf(
				"эталонный реестр: не найдено место %s %s %s уровня %s с форматом %q",
				ref.file, ref.owner, ref.callee, ref.level, ref.format))
		}
	}
	return errs
}

// guardRefPlaceMatches сравнивает место с записью реестра по файлу,
// функции, уровню, формату и аргументам (выражение и тип).
func guardRefPlaceMatches(place *guardPlace, ref *guardRefPlace) bool {
	return place.file == ref.file &&
		place.owner == ref.owner &&
		place.guardLevel == ref.level &&
		place.format == ref.format &&
		slices.Equal(place.args, ref.args)
}

// guardFailedAETrace — вызов failedAETrace и его родитель.
type guardFailedAETrace struct {
	call   *ast.CallExpr
	parent ast.Node
}

// collectFailedAETrace собирает вызовы failedAETrace в дереве.
func collectFailedAETrace(pkg *guardPackage) []guardFailedAETrace {
	var calls []guardFailedAETrace
	for _, file := range pkg.files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "failedAETrace" {
				calls = append(calls, guardFailedAETrace{call: call, parent: pkg.parents[call]})
			}
			return true
		})
	}
	return calls
}

// checkFailedAETrace требует ровно один вызов failedAETrace, и он обязан
// быть непосредственным аргументом обёрнутого вызова ступени репликации:
// вычисление не должно происходить до guard.
func checkFailedAETrace(pkg *guardPackage, places []*guardPlace) []string {
	var errs []string
	calls := collectFailedAETrace(pkg)
	if len(calls) != 1 {
		errs = append(errs, fmt.Sprintf("failedAETrace: вызовов %d, ожидался 1", len(calls)))
	}
	for _, failed := range calls {
		var host *guardPlace
		for _, place := range places {
			if place.call == failed.parent {
				host = place
				break
			}
		}
		if host == nil {
			errs = append(errs, "failedAETrace: не является непосредственным аргументом вызова трассировки — вычисляется до guard")
			continue
		}
		if !host.wrapped || host.guardKind != "traceEnabled" || host.guardLevel != "_traceLevelReplication" {
			errs = append(errs, fmt.Sprintf(
				"failedAETrace: обёртка wrapped=%v kind=%q уровень=%q, ожидался traceEnabled(_traceLevelReplication)",
				host.wrapped, host.guardKind, host.guardLevel))
		}
	}
	return errs
}

// guardKVCall — место KV-трассировки: формат и признак обёртки.
type guardKVCall struct {
	format  string
	wrapped bool
	sole    bool
}

// guardFindMethodDecl ищет метод *ConsensusModule с заданным именем
// в производственных файлах корневого пакета.
func guardFindMethodDecl(pkg *guardPackage, name string) *ast.FuncDecl {
	for _, file := range pkg.files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Name.Name != name {
				continue
			}
			pkgName, recv := guardReceiverNamed(pkg.info.Types[fn.Recv.List[0].Type].Type)
			if pkgName == "raft" && recv == "ConsensusModule" {
				return fn
			}
		}
	}
	return nil
}

// guardIdentNamed сообщает, является ли выражение идентификатором
// с заданным именем.
func guardIdentNamed(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

// guardEnqueueCallStmt сообщает, является ли оператор единственным
// выражением прямого вызова cm.<name>(_traceWriter, …): постановочный
// вызов обёртки с процессным писателем первым аргументом.
func guardEnqueueCallStmt(stmt ast.Stmt, name string) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	if !guardIdentNamed(sel.X, "cm") {
		return false
	}
	return len(call.Args) >= 1 && guardIdentNamed(call.Args[0], "_traceWriter")
}

// guardCMMuCall сообщает, является ли выражение вызовом cm.mu.<name>()
// без аргументов.
func guardCMMuCall(expr ast.Expr, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	mu, ok := sel.X.(*ast.SelectorExpr)
	if !ok || mu.Sel.Name != "mu" || !guardIdentNamed(mu.X, "cm") {
		return false
	}
	return true
}

// guardUnconditionalLockStmt сообщает, что тело начинается с безусловного
// захвата cm.mu и отложенного снятия сразу после него.
func guardUnconditionalLockStmt(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) < 2 {
		return false
	}
	lock, ok := body.List[0].(*ast.ExprStmt)
	if !ok || !guardCMMuCall(lock.X, "Lock") {
		return false
	}
	unlock, ok := body.List[1].(*ast.DeferStmt)
	return ok && guardCMMuCall(unlock.Call, "Unlock")
}

// guardSoleEnqueueStmt сообщает, что блок завершается ровно одним прямым
// вызовом cm.<name>(_traceWriter, …): последний оператор — этот вызов,
// и других таких вызовов в блоке нет. Для тела traceLogf это допускает
// захват и отложенное снятие блокировки перед единственной постановкой.
func guardSoleEnqueueStmt(block *ast.BlockStmt, name string) bool {
	if block == nil || len(block.List) == 0 {
		return false
	}
	if !guardEnqueueCallStmt(block.List[len(block.List)-1], name) {
		return false
	}
	count := 0
	ast.Inspect(block, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == name && guardIdentNamed(sel.X, "cm") {
			count++
		}
		return true
	})
	return count == 1
}

// guardBlockHasConditional сообщает, есть ли в блоке условный оператор.
func guardBlockHasConditional(block *ast.BlockStmt) bool {
	found := false
	ast.Inspect(block, func(n ast.Node) bool {
		if _, ok := n.(*ast.IfStmt); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// checkTraceWrapperBodies проверяет контракт тел трёх обёрток
// трассировки: traceLogf — входная проверка level < _traceCM и
// единственный прямой вызов enqueueTraceLocked; Locked-хелперы — один
// вызов enqueue-метода без условных операторов. Тела проверяются
// отдельными правилами, а не счётчиком прикладных мест.
func checkTraceWrapperBodies(pkg *guardPackage) []string {
	var errs []string

	plain := guardFindMethodDecl(pkg, "traceLogf")
	if plain == nil {
		errs = append(errs, "метод (*ConsensusModule).traceLogf не найден")
	} else {
		if len(plain.Body.List) != 3 {
			errs = append(errs, "тело traceLogf: ожидались три оператора — безусловный Lock, defer Unlock и прямой enqueue")
		}
		if !guardUnconditionalLockStmt(plain.Body) {
			errs = append(errs, "тело traceLogf: ожидались безусловные cm.mu.Lock() и defer cm.mu.Unlock() сразу после")
		}
		if !guardSoleEnqueueStmt(plain.Body, "enqueueTraceLocked") {
			errs = append(errs,
				"тело traceLogf: ожидался единственный завершающий вызов cm.enqueueTraceLocked(_traceWriter, format, args...)")
		}
		if guardBlockHasConditional(plain.Body) {
			errs = append(errs, "тело traceLogf: условные операторы недопустимы")
		}
	}

	checkLockedBody := func(fn *ast.FuncDecl, name, enqueue string) {
		if fn == nil {
			errs = append(errs, fmt.Sprintf("метод (*ConsensusModule).%s не найден", name))
			return
		}
		if len(fn.Body.List) != 1 || !guardEnqueueCallStmt(fn.Body.List[0], enqueue) {
			errs = append(errs, fmt.Sprintf(
				"тело %s: ожидался единственный прямой вызов cm.%s(_traceWriter, format, args...)",
				name, enqueue))
		}
		if guardBlockHasConditional(fn.Body) {
			errs = append(errs, fmt.Sprintf("тело %s: условные операторы недопустимы", name))
		}
	}
	checkLockedBody(guardFindMethodDecl(pkg, "traceLogfLocked"), "traceLogfLocked", "enqueueTraceLocked")
	checkLockedBody(guardFindMethodDecl(pkg, "traceSprintfLocked"), "traceSprintfLocked", "enqueueTraceSprintfLocked")
	return errs
}

// guardDirectEnqueue — прямой вызов enqueue-метода в производственном
// файле: имя файла и функция-владелец.
type guardDirectEnqueue struct {
	file   string
	owner  string
	callee string
}

// guardEnqueueOwners — функции, в телах которых прямые вызовы
// enqueueTrace*Locked разрешены: три обёртки трассировки.
var guardEnqueueOwners = map[string]bool{
	"(*ConsensusModule).traceLogf":          true,
	"(*ConsensusModule).traceLogfLocked":    true,
	"(*ConsensusModule).traceSprintfLocked": true,
}

// collectDirectEnqueues собирает прямые вызовы enqueueTraceLocked и
// enqueueTraceSprintfLocked в производственных файлах корневого пакета.
// Тестовые файлы не рассматриваются: разбор идёт по списку
// production-файлов go list.
func collectDirectEnqueues(pkg *guardPackage) []guardDirectEnqueue {
	var calls []guardDirectEnqueue
	for _, file := range pkg.files {
		base := filepath.Base(pkg.fset.Position(file.Pos()).Filename)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if name != "enqueueTraceLocked" && name != "enqueueTraceSprintfLocked" {
				return true
			}
			if !guardIdentNamed(sel.X, "cm") {
				return true
			}
			calls = append(calls, guardDirectEnqueue{
				file:   base,
				owner:  guardOwnerName(pkg, call),
				callee: name,
			})
			return true
		})
	}
	return calls
}

// checkDirectEnqueues требует, чтобы прямые вызовы enqueueTrace*Locked
// в производственных файлах встречались только в телах трёх обёрток:
// прикладные места обязаны проходить через guard и хелперы.
func checkDirectEnqueues(pkg *guardPackage) []string {
	var errs []string
	for _, call := range collectDirectEnqueues(pkg) {
		if !guardEnqueueOwners[call.owner] {
			errs = append(errs, fmt.Sprintf(
				"%s: прямой вызов %s вне трёх обёрток трассировки (владелец %s)",
				call.file, call.callee, call.owner))
		}
	}
	return errs
}

// collectKVTraceCalls синтаксически собирает вызовы kvs.traceLogf.
func collectKVTraceCalls(t *testing.T, path string) []guardKVCall {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("разбор %s: %v", path, err)
	}
	parents := guardParentsFor([]*ast.File{file})
	var calls []guardKVCall
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "traceLogf" || len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		format, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("разбор формата KV-вызова: %v", err)
		}
		wrapped, sole := false, false
		stmt, ok := parents[call].(*ast.ExprStmt)
		if ok {
			if block, ok := parents[stmt].(*ast.BlockStmt); ok {
				if ifStmt, ok := parents[block].(*ast.IfStmt); ok && ifStmt.Body == block {
					sole = len(block.List) == 1
					wrapped = guardGuardKVCondition(ifStmt.Cond)
				}
			}
		}
		calls = append(calls, guardKVCall{format: format, wrapped: wrapped, sole: sole})
		return true
	})
	return calls
}

// guardGuardKVCondition распознаёт условие if _traceKV > 0.
func guardGuardKVCondition(cond ast.Expr) bool {
	binary, ok := cond.(*ast.BinaryExpr)
	if !ok || binary.Op != token.GTR {
		return false
	}
	id, ok := binary.X.(*ast.Ident)
	if !ok || id.Name != "_traceKV" {
		return false
	}
	lit, ok := binary.Y.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "0"
}

// checkKVTraceGuards проверяет пять обёрнутых elapsed-мест и шесть
// необёрнутых вызовов KV-трассировки.
func checkKVTraceGuards(t *testing.T) []string {
	t.Helper()
	calls := collectKVTraceCalls(t, filepath.Join("pkg", "kvservice", "kvservice.go"))
	var errs []string
	expected := map[string]bool{}
	for _, format := range kvGuardFormats {
		expected[format] = true
	}
	seen := map[string]bool{}
	wrapped := 0
	for _, call := range calls {
		if expected[call.format] {
			wrapped++
			seen[call.format] = true
			if !call.wrapped || !call.sole {
				errs = append(errs, fmt.Sprintf(
					"KV %q: ожидалась обёртка if _traceKV > 0 (wrapped=%v sole=%v)",
					call.format, call.wrapped, call.sole))
			}
			continue
		}
		if call.wrapped {
			errs = append(errs, fmt.Sprintf("KV %q: вызов вне охвата не должен быть обёрнут", call.format))
		}
	}
	if len(calls) != 11 {
		errs = append(errs, fmt.Sprintf("KV: вызовов %d, ожидалось 11", len(calls)))
	}
	if wrapped != len(kvGuardFormats) {
		errs = append(errs, fmt.Sprintf("KV: обёрнуто %d мест, ожидалось %d", wrapped, len(kvGuardFormats)))
	}
	for _, format := range kvGuardFormats {
		if !seen[format] {
			errs = append(errs, fmt.Sprintf("KV: не найдено обёрнутое место с форматом %q", format))
		}
	}
	return errs
}

// TestTraceGuards — постоянный AST-гейт guards, тел обёрток, прямых
// enqueue-вызовов и ссылочных аргументов.
func TestTraceGuards(t *testing.T) {
	pkg := parseGuardPackage(t)
	places := collectGuardPlaces(pkg)
	baselineErrs := checkGuardPlaces(places)
	baselineErrs = append(baselineErrs, checkFailedAETrace(pkg, places)...)
	baselineErrs = append(baselineErrs, checkTraceWrapperBodies(pkg)...)
	baselineErrs = append(baselineErrs, checkDirectEnqueues(pkg)...)
	if len(baselineErrs) > 0 {
		t.Fatalf("гейт guards провален на production-коде:\n  - %s", strings.Join(baselineErrs, "\n  - "))
	}

	t.Run("kv", func(t *testing.T) {
		errs := checkKVTraceGuards(t)
		if len(errs) > 0 {
			t.Fatalf("гейт guards KV провален:\n  - %s", strings.Join(errs, "\n  - "))
		}
	})

	t.Run("negative probes", func(t *testing.T) {
		probes := []struct {
			name  string
			apply func(pkg *guardPackage, places []*guardPlace) (restore func(), ok bool)
		}{
			{name: "wrong-level", apply: guardProbeWrongLevel},
			{name: "wrong-level-w", apply: guardProbeWrongLevelW},
			{name: "wrong-level-l", apply: guardProbeWrongLevelL},
			{name: "wrong-branch", apply: guardProbeWrongBranch},
			{name: "new-map-async", apply: guardProbeNewMapAsync},
			{name: "sprintf-demoted", apply: guardProbeSprintfDemoted},
			{name: "old-signature-w", apply: guardProbeOldSignatureW},
			{name: "old-signature-l", apply: guardProbeOldSignatureL},
			{name: "old-signature-s", apply: guardProbeOldSignatureS},
			{name: "plain-no-lock", apply: guardProbePlainNoLock},
			{name: "plain-no-unlock", apply: guardProbePlainNoUnlock},
			{name: "plain-level-check", apply: guardProbePlainLevelCheck},
			{name: "direct-enqueue", apply: guardProbeDirectEnqueue},
		}
		for _, probe := range probes {
			t.Run(probe.name, func(t *testing.T) {
				restore, ok := probe.apply(pkg, places)
				if !ok {
					t.Fatalf("проба %q: не найдено место для мутации", probe.name)
				}
				defer restore()
				mutated := collectGuardPlaces(pkg)
				errs := checkGuardPlaces(mutated)
				errs = append(errs, checkFailedAETrace(pkg, mutated)...)
				errs = append(errs, checkTraceWrapperBodies(pkg)...)
				errs = append(errs, checkDirectEnqueues(pkg)...)
				if len(errs) == 0 {
					t.Fatalf("гейт не отверг отрицательный пример %q", probe.name)
				}
				t.Logf("пример %q отвергнут: %s", probe.name, strings.Join(errs, "; "))
			})
		}
	})
}

// guardProbeWrongLevel подменяет уровень в guard первого асинхронного
// вызова: место обязано выпасть из эталонного реестра уровней и форматов.
func guardProbeWrongLevel(_ *guardPackage, places []*guardPlace) (func(), bool) {
	return guardProbeGuardLevelMutation(places, "traceLogf")
}

// guardProbeWrongLevelW подменяет допустимый уровень guard'а первого
// места traceLogf при неизменных формате, количестве вызовов и структуре
// обёртки: эталонный реестр обязан отвергнуть место.
func guardProbeWrongLevelW(_ *guardPackage, places []*guardPlace) (func(), bool) {
	return guardProbeGuardLevelMutation(places, "traceLogf")
}

// guardProbeWrongLevelL — то же для первого места traceLogfLocked.
func guardProbeWrongLevelL(_ *guardPackage, places []*guardPlace) (func(), bool) {
	return guardProbeGuardLevelMutation(places, "traceLogfLocked")
}

// guardProbeGuardLevelMutation подменяет уровень guard'а первого места
// заданного метода на другой допустимый уровень.
func guardProbeGuardLevelMutation(places []*guardPlace, callee string) (func(), bool) {
	for _, place := range places {
		if place.guardCall == nil || place.callee != callee {
			continue
		}
		original := place.guardCall.Args[0]
		replacement := "_traceLevelLoops"
		if guardIdentNamed(original, replacement) {
			replacement = "_traceLevelKeyEvents"
		}
		place.guardCall.Args[0] = ast.NewIdent(replacement)
		return func() { place.guardCall.Args[0] = original }, true
	}
	return nil, false
}

// guardProbeOldSignatureW возвращает первому месту traceLogf первый
// аргумент-уровень старой сигнатуры: формат места обязан перестать
// совпадать с эталонным реестром.
func guardProbeOldSignatureW(_ *guardPackage, places []*guardPlace) (func(), bool) {
	return guardProbeOldSignature(places, "traceLogf")
}

// guardProbeOldSignatureL — то же для traceLogfLocked.
func guardProbeOldSignatureL(_ *guardPackage, places []*guardPlace) (func(), bool) {
	return guardProbeOldSignature(places, "traceLogfLocked")
}

// guardProbeOldSignatureS — то же для traceSprintfLocked.
func guardProbeOldSignatureS(_ *guardPackage, places []*guardPlace) (func(), bool) {
	return guardProbeOldSignature(places, "traceSprintfLocked")
}

// guardProbeOldSignature добавляет первому месту заданного метода
// аргумент-уровень перед форматом.
func guardProbeOldSignature(places []*guardPlace, callee string) (func(), bool) {
	for _, place := range places {
		if place.callee != callee {
			continue
		}
		original := place.call.Args
		place.call.Args = append(
			[]ast.Expr{ast.NewIdent("_traceLevelKeyEvents")},
			slices.Clone(original)...,
		)
		return func() { place.call.Args = original }, true
	}
	return nil, false
}

// guardProbePlainNoLock удаляет безусловный захват cm.mu из тела
// traceLogf: правило тела обязано отвергнуть мутацию.
func guardProbePlainNoLock(pkg *guardPackage, _ []*guardPlace) (func(), bool) {
	return guardProbePlainBodyMutation(pkg, func(list []ast.Stmt) []ast.Stmt {
		return slices.Clone(list[1:])
	})
}

// guardProbePlainNoUnlock удаляет отложенное снятие cm.mu из тела
// traceLogf: правило тела обязано отвергнуть мутацию.
func guardProbePlainNoUnlock(pkg *guardPackage, _ []*guardPlace) (func(), bool) {
	return guardProbePlainBodyMutation(pkg, func(list []ast.Stmt) []ast.Stmt {
		mutated := slices.Clone(list)
		return append(mutated[:1], mutated[2:]...)
	})
}

// guardProbePlainLevelCheck возвращает сравнение уровня в тело traceLogf:
// условный оператор с порогом обязан быть отвергнут правилом тела.
func guardProbePlainLevelCheck(pkg *guardPackage, _ []*guardPlace) (func(), bool) {
	return guardProbePlainBodyMutation(pkg, func(list []ast.Stmt) []ast.Stmt {
		return []ast.Stmt{&ast.IfStmt{
			Cond: &ast.BinaryExpr{
				X:  ast.NewIdent("level"),
				Op: token.LSS,
				Y:  ast.NewIdent("_traceCM"),
			},
			Body: &ast.BlockStmt{List: slices.Clone(list)},
		}}
	})
}

// guardProbePlainBodyMutation применяет мутацию к телу traceLogf и
// возвращает функцию восстановления исходного тела.
func guardProbePlainBodyMutation(
	pkg *guardPackage, mutate func([]ast.Stmt) []ast.Stmt,
) (func(), bool) {
	fn := guardFindMethodDecl(pkg, "traceLogf")
	if fn == nil || len(fn.Body.List) < 2 {
		return nil, false
	}
	original := fn.Body.List
	fn.Body.List = mutate(original)
	return func() { fn.Body.List = original }, true
}

// guardProbeWrongBranch переносит тело guard в else: вызов перестаёт
// быть положительной ветвью.
func guardProbeWrongBranch(_ *guardPackage, places []*guardPlace) (func(), bool) {
	for _, place := range places {
		if place.ifStmt == nil {
			continue
		}
		originalBody := place.ifStmt.Body.List
		place.ifStmt.Body.List = nil
		place.ifStmt.Else = &ast.BlockStmt{List: originalBody}
		return func() {
			place.ifStmt.Body.List = originalBody
			place.ifStmt.Else = nil
		}, true
	}
	return nil, false
}

// guardProbeNewMapAsync добавляет ссылочный аргумент (карту) в
// асинхронный вызов: тип нового выражения отсутствует, гейт обязан
// отвергнуть и это, и чтение ссылки.
func guardProbeNewMapAsync(_ *guardPackage, places []*guardPlace) (func(), bool) {
	for _, place := range places {
		if place.callee != "traceLogf" || place.guardKind != "traceEnabled" {
			continue
		}
		original := place.call.Args
		place.call.Args = append(slices.Clone(original), &ast.SelectorExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent("cm"),
				Sel: ast.NewIdent("leaderState"),
			},
			Sel: ast.NewIdent("nextIndex"),
		})
		return func() { place.call.Args = original }, true
	}
	return nil, false
}

// guardProbeSprintfDemoted понижает первое ссылочное место до
// traceLogfLocked: ломаются и счёт 48/5/22, и запрет ссылок.
func guardProbeSprintfDemoted(_ *guardPackage, places []*guardPlace) (func(), bool) {
	for _, place := range places {
		sel, ok := place.call.Fun.(*ast.SelectorExpr)
		if !ok || place.callee != "traceSprintfLocked" {
			continue
		}
		sel.Sel.Name = "traceLogfLocked"
		return func() { sel.Sel.Name = "traceSprintfLocked" }, true
	}
	return nil, false
}

// guardProbeDirectEnqueue добавляет прикладной прямой вызов
// enqueueTraceLocked в тело производственной функции вне трёх обёрток:
// правило запрета прямых enqueue обязано отвергнуть мутацию.
func guardProbeDirectEnqueue(pkg *guardPackage, _ []*guardPlace) (func(), bool) {
	fn := guardFindMethodDecl(pkg, "stats")
	if fn == nil {
		return nil, false
	}
	original := fn.Body.List
	fn.Body.List = append(slices.Clone(original), &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent("cm"),
				Sel: ast.NewIdent("enqueueTraceLocked"),
			},
			Args: []ast.Expr{
				ast.NewIdent("_traceWriter"),
				&ast.BasicLit{Kind: token.STRING, Value: `"direct"`},
			},
		},
	})
	return func() { fn.Body.List = original }, true
}
