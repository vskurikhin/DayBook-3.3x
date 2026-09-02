package raft

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// Стресс‑режим для тестирования выборов: если задана переменная окружения,
// функция electionTimeout() в трети случаев возвращает фиксированное значение
// ReelectionTimeoutMs. Это отключает рандомизацию таймаута, чтобы провоцировать
// одновременные попытки запуска выборов и проверять устойчивость алгоритма.
const forcedReelectionEnv = "RAFT_FORCE_MORE_REELECTION"

const (
	// hookSamples — размер выборки значений electionTimeout() в каждой
	// ветви. M = 1000 достаточно для разделения двух распределений
	// с запасом в несколько десятков сигм (обоснование порогов ниже).
	hookSamples = 1000

	// wantHookedShare — нижняя граница доли значений, равных ровно
	// ReelectionTimeoutMs, при ВКЛЮЧЁННОМ хуке. Матожидание доли — 1/3
	// (rand.Intn(3) == 0); при M = 1000 σ = sqrt(p(1-p)/M) ≈ 0.0149,
	// поэтому порог 0.20 отстоит от матожидания примерно на 9σ.
	wantHookedShare = 0.20

	// maxUnhookedShare — верхняя граница той же доли при ВЫКЛЮЧЕННОМ
	// хуке. Там значение ReelectionTimeoutMs выпадает только при
	// rand.Intn(ReelectionTimeoutMs) == 0, то есть с вероятностью
	// 1/381 ≈ 0.26 %; при M = 1000 σ ≈ 0.0016, и порог 0.05 отстоит
	// от матожидания примерно на 30σ.
	maxUnhookedShare = 0.05
)

// TestElectionTimeout_ForcedReelectionHook изолированно проверяет логику стресс‑хука
// RAFT_FORCE_MORE_REELECTION на уровне функции electionTimeout().
//
// Разделение ответственности тестов:
//   - Этот тест подтверждает, что хук корректно влияет на распределение таймаутов.
//   - Реальные последствия (смены лидера, сходимость кластера) проверяются отдельно
//     в TestClusterConvergence_ForcedReelections.
//
// Такой подход позволяет сделать тест быстрым и детерминированным: не требуется
// поднимать кластер или использовать временные задержки. Для проверки достаточно
// минимального экземпляра ConsensusModule.
//
// Мутационное свойство: отключение хука резко снижает долю срабатываний его ветки
// примерно до 1/381. Это простой и надёжный способ убедиться, что изменение кода
// действительно затронуло нужную логику.
func TestElectionTimeout_ForcedReelectionHook(t *testing.T) {
	// t.Setenv несовместим с t.Parallel — тест остаётся serial (§15).
	hooked := time.Duration(ReelectionTimeoutMs) * time.Millisecond

	// Порядок ветвей: сначала контрольная (хук выключен), затем с хуком.
	t.Run("hook disabled", func(t *testing.T) {
		// Пустое значение неотличимо от отсутствия переменной для
		// предиката production-кода (os.Getenv(...) != ""), но, в отличие
		// от него, не зависит от окружения, в котором запущен бинарник.
		t.Setenv(forcedReelectionEnv, "")

		share := hookedShare(t, hooked)
		if share > maxUnhookedShare {
			t.Errorf(
				"без хука доля значений ровно %v = %.4f, want <= %.2f (матожидание 1/%d)",
				hooked, share, maxUnhookedShare, ReelectionTimeoutMs,
			)
		}
	})

	t.Run("hook enabled", func(t *testing.T) {
		t.Setenv(forcedReelectionEnv, "1")

		share := hookedShare(t, hooked)
		if share < wantHookedShare {
			t.Errorf(
				"с хуком доля значений ровно %v = %.4f, want >= %.2f (матожидание 1/3)",
				hooked, share, wantHookedShare,
			)
		}
	})
}

// hookedShare снимает выборку hookSamples значений electionTimeout(),
// проверяет диапазон каждого значения и возвращает долю значений,
// равных ровно hooked.
func hookedShare(t *testing.T, hooked time.Duration) float64 {
	t.Helper()

	minTimeout := time.Duration(ReelectionTimeoutMs) * time.Millisecond
	maxTimeout := time.Duration(2*ReelectionTimeoutMs) * time.Millisecond

	cm := &ConsensusModule{}
	exact := 0
	for i := 0; i < hookSamples; i++ {
		got := cm.electionTimeout()
		if got < minTimeout || got >= maxTimeout {
			t.Fatalf(
				"electionTimeout() = %v вне диапазона [%v, %v) на выборке %d",
				got, minTimeout, maxTimeout, i,
			)
		}
		if got == hooked {
			exact++
		}
	}
	return float64(exact) / float64(hookSamples)
}

// recordingVoteTransport — локальная заглушка транспорта (приём
// blockingPreVoteTransport, prevote_test.go:347-359): встраивает интерфейс
// Transport и переопределяет только RequestVote. Запоминает адресата и полную
// копию RequestVoteArgs, возвращает ответ, не совпадающий по терму и не
// дающий голоса. Остальные методы интерфейса остаются недостижимыми.
type recordingVoteTransport struct {
	Transport
	peerID ServerID
	args   RequestVoteArgs
	calls  int
}

// RequestVote фиксирует исходящие аргументы для последующей проверки
// плетения параметров requestVoteFromPeer.
func (t *recordingVoteTransport) RequestVote(peerID ServerID, args RequestVoteArgs) (RequestVoteReply, error) {
	t.peerID = peerID
	t.args = args
	t.calls++
	return RequestVoteReply{Term: 0, VoteGranted: false}, nil
}

// newRequestVoteFromPeerTestCM собирает узел для прямого вызова
// requestVoteFromPeer: кандидат с двумя голосующими (0, 1), явно заданными
// кэшированными полями журнала. Приём сборки — newPreVoteTestCM
// (prevote_test.go:301-325), но роль — Candidate, votedFor — 0, а
// lastLogIndex/lastLogTerm — явными нетривиальными значениями, попарно
// различными и отличными от peerID, voterCount и savedCurrentTerm, иначе
// перестановка параметров осталась бы незамеченной.
func newRequestVoteFromPeerTestCM(transport Transport, term, lastLogIndex, lastLogTerm int) *ConsensusModule {
	cm := &ConsensusModule{
		id:         0,
		transport:  transport,
		storage:    store.NewMapStorage(),
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			state:        Candidate,
			currentTerm:  term,
			votedFor:     0,
			lastLogIndex: lastLogIndex,
			lastLogTerm:  lastLogTerm,
			configurations: configurations{
				latest: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
			},
		},
	}
	cm.cmState.log = make([]LogEntry, 0)
	return cm
}

// TestRequestVoteFromPeer_OutgoingArgs — белоящичный тест T-1 исходящих
// аргументов RequestVote, формируемых requestVoteFromPeer. Страж мутации 2
// AC-7 (плетение параметров пятиместной сигнатуры).
//
// Без кластера, без сети, без таймеров и без сна: requestVoteFromPeer
// вызывается синхронно из горутины теста. Ответ заглушки (Term: 0,
// VoteGranted: false) не совпадает по терму и не даёт голоса, поэтому разбор
// ответа не меняет состояние, не вызывает becomeFollowerLocked/startLeaderLocked
// и не порождает горутин.
func TestRequestVoteFromPeer_OutgoingArgs(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	const (
		// savedCurrentTerm — терм выборов, отличный от нуля и от peerID.
		savedCurrentTerm = 5
		peerID           = 1
		voterCount       = 2
		// lastLogIndex/lastLogTerm — попарно различные и отличные от
		// peerID, voterCount и savedCurrentTerm, чтобы перестановка
		// параметров осталась замеченной.
		lastLogIndex = 7
		lastLogTerm  = 3
	)

	// Оба подслучая обязательны: передача лидерства и обычные выборы.
	subtests := []struct {
		name                 string
		isLeadershipTransfer bool
		wantLT               bool
	}{
		{name: "leadership transfer", isLeadershipTransfer: true, wantLT: true},
		{name: "обычные выборы", isLeadershipTransfer: false, wantLT: false},
	}

	for _, tc := range subtests {
		t.Run(tc.name, func(t *testing.T) {
			transport := &recordingVoteTransport{}
			cm := newRequestVoteFromPeerTestCM(transport, savedCurrentTerm, lastLogIndex, lastLogTerm)

			// Ожидаемые значения журнала читаем под cm.mu: lastLogIndexAndTermLocked
			// требует удержания блокировки (raft_cm_log.go:12).
			cm.mu.Lock()
			wantLastLogIndex, wantLastLogTerm := cm.lastLogIndexAndTermLocked()
			cm.mu.Unlock()

			var votesReceived atomic.Int32
			votesReceived.Store(1)

			cm.requestVoteFromPeer(peerID, savedCurrentTerm, voterCount, tc.isLeadershipTransfer, &votesReceived)

			// Общие утверждения обоих подслучаев (плетение параметров).
			if transport.calls != 1 {
				t.Fatalf("transport вызван %d раз, want 1", transport.calls)
			}
			if transport.peerID != ServerID(peerID) {
				t.Errorf("адресат = %v, want %v", transport.peerID, ServerID(peerID))
			}
			if transport.args.Term != savedCurrentTerm {
				t.Errorf("args.Term = %d, want %d", transport.args.Term, savedCurrentTerm)
			}
			if transport.args.CandidateID != cm.id {
				t.Errorf("args.CandidateID = %d, want %d", transport.args.CandidateID, cm.id)
			}
			if transport.args.RPCHeader.ServerID != cm.id {
				t.Errorf("args.RPCHeader.ServerID = %d, want %d", transport.args.RPCHeader.ServerID, cm.id)
			}
			if transport.args.RPCHeader.ProtocolVersion != ProtocolVersion {
				t.Errorf(
					"args.RPCHeader.ProtocolVersion = %d, want %d",
					transport.args.RPCHeader.ProtocolVersion, ProtocolVersion,
				)
			}
			if transport.args.LastLogIndex != wantLastLogIndex {
				t.Errorf("args.LastLogIndex = %d, want %d", transport.args.LastLogIndex, wantLastLogIndex)
			}
			if transport.args.LastLogTerm != wantLastLogTerm {
				t.Errorf("args.LastLogTerm = %d, want %d", transport.args.LastLogTerm, wantLastLogTerm)
			}
			if transport.args.LeadershipTransfer != tc.wantLT {
				t.Errorf(
					"args.LeadershipTransfer = %v, want %v",
					transport.args.LeadershipTransfer, tc.wantLT,
				)
			}
		})
	}
}
