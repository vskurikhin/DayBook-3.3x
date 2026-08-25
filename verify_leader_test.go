package raft

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// TestVerifyLeader_TwoNodes_FollowerDownNoQuorum — пороговый тест n=2:
// при отключённом follower'е подтверждение лидерства НЕ наступает
// (порог quorumSize(2)=2, есть только self-голос); после восстановления
// связи — наступает после первого ack'а follower'а.
//
// Старый код (порог len(peerIds)/2+1 = 1) подтверждал бы лидерство
// в одиночку — тест дискриминирует.
func TestVerifyLeader_TwoNodes_FollowerDownNoQuorum(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 2)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	follower := (lid + 1) % 2
	h.DisconnectPeer(follower)

	future := h.cluster[lid].VerifyLeader()

	// Контрольное окно с запасом на несколько heartbeat-интервалов:
	// подтверждение не должно наступить без ack'а follower'а.
	select {
	case err := <-future.ErrorCh():
		t.Fatalf("VerifyLeader confirmed without follower ack (err=%v)", err)
	case <-time.After(HeartbeatTimeoutMs * 5 * time.Millisecond):
	}

	h.ReconnectPeer(follower)
	if err := waitFuture(t, future, 3*time.Second); err != nil {
		t.Fatalf("VerifyLeader failed after reconnect: %v", err)
	}
}

// TestVerifyLeader_FourNodes_OneFollowerNotEnough — порог 3/4:
// self + один подключённый follower (2/4) НЕ подтверждают лидерство,
// сколько бы повторных подтверждений пульса ни прислал тот же follower
// (дедупликация голосов по peer). После подключения второго follower'а
// (3/4 = кворум) подтверждение наступает.
//
// Старый код (порог len(peerIds)/2+1 = 2) подтверждал бы при self + 1
// follower — тест дискриминирует.
func TestVerifyLeader_FourNodes_OneFollowerNotEnough(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отключить двух follower'ов; один остаётся подключённым.
	down1 := (lid + 1) % 4
	down2 := (lid + 2) % 4
	h.DisconnectPeer(down1)
	h.DisconnectPeer(down2)

	future := h.cluster[lid].VerifyLeader()

	// Негативное окно с запасом на несколько heartbeat-тиков:
	// повторные ack'и одного follower'а не накапливаются.
	select {
	case err := <-future.ErrorCh():
		t.Fatalf("VerifyLeader confirmed with 2/4 votes (err=%v)", err)
	case <-time.After(HeartbeatTimeoutMs * 5 * time.Millisecond):
	}

	// Подключить второго follower'а — 3/4 = кворум.
	h.ReconnectPeer(down1)
	if err := waitFuture(t, future, 3*time.Second); err != nil {
		t.Fatalf("VerifyLeader failed with 3/4 alive: %v", err)
	}
}

// TestVerifyLeader_FourNodes_OneDownStillVerifies — позитивный контроль:
// при одном отключённом follower (живы 3/4) подтверждение наступает.
// Окно ожидания включает несколько heartbeat-интервалов
// (подтверждение может задержаться до следующего тика heartbeat из-за
// занятого inflightAE).
func TestVerifyLeader_FourNodes_OneDownStillVerifies(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	down := (lid + 1) % 4
	h.DisconnectPeer(down)

	future := h.cluster[lid].VerifyLeader()
	if err := waitFuture(t, future, 3*time.Second); err != nil {
		t.Fatalf("VerifyLeader failed with 3/4 alive: %v", err)
	}
}

// TestVerifyLeader_NonvoterAckDoesNotVote — негатив по неголосующему:
// после DemoteVoter живы только лидер и неголосующий с непрерывными
// ack'ами — подтверждение НЕ наступает: ack неголосующего голосом не
// является, порог 2 голосов (3 voters) собирается только из голосующих.
//
// Старый код голосовал на каждый success-ответ без фильтра Suffrage —
// self + неголосующий давали бы 2 голоса и ложное подтверждение.
func TestVerifyLeader_NonvoterAckDoesNotVote(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Демотировать одного follower'а в неголосующего и дождаться фиксации
	// конфигурации (дедлайн-поллинг GetConfiguration).
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := waitFuture(t, f, 3*time.Second); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	demoted := false
	for time.Now().Before(deadline) && !demoted {
		cfg := h.cluster[lid].GetConfiguration().Configuration()
		for _, s := range cfg.ConfigServers {
			if s.ID == ServerID(demoteID) {
				demoted = s.Suffrage == Nonvoter
			}
		}
		if !demoted {
			// poll-интервал condition-wait (не фиксированная пауза).
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !demoted {
		t.Fatalf("server %d not demoted to nonvoter in time", demoteID)
	}

	// Отключить остальных голосующих follower'ов: живы лидер + неголосующий.
	for i := 0; i < h.n; i++ {
		if i == lid || i == demoteID {
			continue
		}
		h.DisconnectPeer(i)
	}

	future := h.cluster[lid].VerifyLeader()

	// Негативное окно с запасом: ack'и неголосующего не голосуют.
	select {
	case err := <-future.ErrorCh():
		t.Fatalf("VerifyLeader confirmed by nonvoter acks (err=%v)", err)
	case <-time.After(HeartbeatTimeoutMs * 5 * time.Millisecond):
	}
}

// TestVerifyFuture_VoteDeduplication — белый короб на verifyFuture.vote:
// повторные голоса одного peerID не накапливаются.
// Прямая проверка счётчика: два голоса одного узла дают votes=1,
// голос другого узла — votes=2.
func TestVerifyFuture_VoteDeduplication(t *testing.T) {
	vf := &verifyFuture{
		voted:      make(map[int]struct{}),
		quorumSize: 2,
	}

	vf.vote(1, true)
	vf.vote(1, true) // повторное подтверждение пульса того же узла — no-op
	if vf.votes != 1 {
		t.Fatalf("votes = %d, want 1 (duplicate vote must be ignored)", vf.votes)
	}

	vf.vote(2, true)
	if vf.votes != 2 {
		t.Fatalf("votes = %d, want 2", vf.votes)
	}
}

// TestVerifyLeader_EpochFilter: голос учитывается только если AE отправлен
// не раньше постановки запроса (vf.epoch <= dispatchEpoch).
func TestVerifyLeader_EpochFilter(t *testing.T) {
	newCM := func(epoch uint64) (*ConsensusModule, *verifyFuture) {
		cm := &ConsensusModule{
			id: 0,
			// Терм ответа совпадает с термом лидера: отправка выполняется
			// в текущем терме, иначе успешный ответ к состоянию репликации
			// не применяется и фильтр по раунду верификации не проверялся бы.
			transport: &mockTransportAE{replyTerm: 1},
			commitCh:  make(chan int, 1),
			leaderState: leaderState{
				nextIndex:  map[int]int{1: 0},
				matchIndex: map[int]int{1: -1},
				inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
			},
			cmState: cmState{
				state:        Leader,
				log:          []LogEntry{{Index: 0, Term: 1}},
				lastLogIndex: 0,
				currentTerm:  1,
				commitIndex:  -1,
				configurations: configurations{
					committed: Configuration{ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					}},
					latest: Configuration{ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					}},
				},
			},
		}
		cm.leaderState.commitmentTracker = newCommitmentTracker(cm.id, 1, 0, cm.commitCh)
		cm.leaderState.commitmentTracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)
		vf := &verifyFuture{
			voted:      make(map[int]struct{}),
			quorumSize: 2,
			epoch:      epoch,
		}
		cm.leaderState.pendingVerify = []*verifyFuture{vf}
		return cm, vf
	}

	// Ответ AE, отправленного ДО запроса (dispatchEpoch=1 < epoch=2):
	// голос не засчитывается.
	cm, vf := newCM(2)
	cm.leaderSendAEsToPeer(1, 1, 1, true)
	if vf.votes != 0 {
		t.Fatalf("votes = %d, want 0 (AE dispatched before request must not vote)", vf.votes)
	}

	// Ответ AE, отправленного ПОСЛЕ запроса (dispatchEpoch=2 == epoch=2):
	// голос засчитывается.
	cm, vf = newCM(2)
	cm.leaderSendAEsToPeer(1, 1, 2, true)
	if vf.votes != 1 {
		t.Fatalf("votes = %d, want 1 (AE dispatched after request must vote)", vf.votes)
	}
}

// TestVerifyLeader_MultiplePendingDifferentEpochs — фильтр по раунду
// верификации применяется к каждому ожидающему запросу отдельно.
//
// dispatchEpoch — раунд, снятый в момент отправки AppendEntries. Голос
// засчитывается только тем запросам верификации, которые были поставлены не
// позже отправки (epoch запроса не больше dispatchEpoch). Набравшие кворум
// запросы покидают очередь, остальные сохраняются в исходном порядке.
func TestVerifyLeader_MultiplePendingDifferentEpochs(t *testing.T) {
	mock := &mockTransportAE{replyTerm: 1}
	cm := &ConsensusModule{
		id:         0,
		transport:  mock,
		commitCh:   make(chan int, 1),
		shutdownCh: make(chan struct{}),
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 0},
			matchIndex: map[int]int{1: -1},
			inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastLogIndex:      0,
			lastLogTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			commitIndex:       -1,
			termIndexMap:      map[int]int{1: 0},
			log:               []LogEntry{{Index: 0, Term: 1}},
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{
					{ID: 0, Suffrage: Voter},
					{ID: 1, Suffrage: Voter},
					{ID: 2, Suffrage: Voter},
				}},
				latest: Configuration{ConfigServers: []ConfigServer{
					{ID: 0, Suffrage: Voter},
					{ID: 1, Suffrage: Voter},
					{ID: 2, Suffrage: Voter},
				}},
			},
		},
	}
	cm.leaderState.inflightAE[1].Store(false)
	cm.leaderState.commitmentTracker = newCommitmentTracker(cm.id, 1, 0, cm.commitCh)
	cm.leaderState.commitmentTracker.setConfiguration([]int{0, 1, 2}, cm.lookupTermLocked)

	// newVerifyFuture — запрос верификации с уже учтённым голосом лидера.
	newVerifyFuture := func(epoch uint64, quorumSize int) *verifyFuture {
		vf := &verifyFuture{
			voted:      map[int]struct{}{cm.id: {}},
			votes:      1,
			quorumSize: quorumSize,
			epoch:      epoch,
		}
		vf.init(cm.shutdownCh)
		return vf
	}
	vfOld := newVerifyFuture(3, 2)
	vfSame := newVerifyFuture(5, 3)
	vfFuture := newVerifyFuture(7, 2)
	cm.leaderState.pendingVerify = []*verifyFuture{vfOld, vfSame, vfFuture}

	cm.leaderSendAEsToPeer(1, 1, 5, true)

	if err := vfOld.Error(); err != nil {
		t.Fatalf("vfOld.Error() = %v, want nil (quorum reached, epoch 3 <= 5)", err)
	}
	if vfSame.votes != 2 {
		t.Fatalf("vfSame.votes = %d, want 2 (epoch 5 <= 5 votes, quorum 3 not reached)", vfSame.votes)
	}
	if vfFuture.votes != 1 {
		t.Fatalf("vfFuture.votes = %d, want 1 (epoch 7 > 5 must not vote)", vfFuture.votes)
	}
	remaining := cm.leaderState.pendingVerify
	if len(remaining) != 2 || remaining[0] != vfSame || remaining[1] != vfFuture {
		t.Fatalf("pendingVerify = %v, want [vfSame vfFuture] in the original order", remaining)
	}
}

// TestVerifyLeader_NoVoteOnFailureAndSnapshotPath фиксирует существующее
// консервативное поведение: подтверждением лидерства считается только
// успешный ответ AppendEntries. Отказ ведомого (Success: false) и путь
// отправки снимка голосом не являются, запрос остаётся в очереди.
//
// Вопрос о том, следует ли считать такие контакты подтверждением лидерства,
// за пределами данной проверки и производственным кодом не решается.
func TestVerifyLeader_NoVoteOnFailureAndSnapshotPath(t *testing.T) {
	// newCM собирает литерального лидера с одним ожидающим запросом
	// верификации: голос лидера уже учтён, для кворума нужен ещё один.
	newCM := func(transport Transport, snapshotStore SnapshotStore, lastSnapshotIndex, nextIndex int, log []LogEntry) (*ConsensusModule, *verifyFuture) {
		cm := &ConsensusModule{
			id:         0,
			transport:  transport,
			commitCh:   make(chan int, 1),
			shutdownCh: make(chan struct{}),
			leaderState: leaderState{
				nextIndex:  map[int]int{1: nextIndex},
				matchIndex: map[int]int{1: -1},
				inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
			},
			cmState: cmState{
				state:             Leader,
				currentTerm:       1,
				lastLogIndex:      5,
				lastLogTerm:       1,
				lastSnapshotIndex: lastSnapshotIndex,
				lastSnapshotTerm:  1,
				commitIndex:       -1,
				termIndexMap:      map[int]int{1: 5},
				log:               log,
				configurations: configurations{
					committed: Configuration{ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					}},
					latest: Configuration{ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					}},
				},
			},
			snapshotStore: snapshotStore,
		}
		cm.leaderState.inflightAE[1].Store(false)
		cm.leaderState.commitmentTracker = newCommitmentTracker(cm.id, 1, 0, cm.commitCh)
		cm.leaderState.commitmentTracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)
		vf := &verifyFuture{
			voted:      map[int]struct{}{cm.id: {}},
			votes:      1,
			quorumSize: 2,
			epoch:      0,
		}
		vf.init(cm.shutdownCh)
		cm.leaderState.pendingVerify = []*verifyFuture{vf}
		return cm, vf
	}

	t.Run("логический отказ ведомого голосом не является", func(t *testing.T) {
		mock := &mockTransportAE{failReply: true, failConflictIndex: 8, failConflictTerm: -1, replyTerm: 1}
		cm, vf := newCM(mock, nil, -1, 5, []LogEntry{{Index: 4, Term: 1}, {Index: 5, Term: 1}})

		cm.leaderSendAEsToPeer(1, 1, 0, true)

		if got := mock.callCount.Load(); got != 1 {
			t.Fatalf("AppendEntries calls = %d, want 1", got)
		}
		if vf.votes != 1 {
			t.Fatalf("votes = %d, want 1 (rejection must not vote)", vf.votes)
		}
		if len(cm.leaderState.pendingVerify) != 1 || cm.leaderState.pendingVerify[0] != vf {
			t.Fatalf("pendingVerify = %v, want the request still queued", cm.leaderState.pendingVerify)
		}
		select {
		case err := <-vf.ErrorCh():
			t.Fatalf("verify request completed with %v, want still pending", err)
		default:
		}
	})

	t.Run("путь снимка голосом не является", func(t *testing.T) {
		// installReplyTerm = 0 не совпадает с термом запроса, поэтому ответ
		// на снимок состояние репликации не изменяет: проверяется только
		// отсутствие голоса.
		mock := &mockTransportAE{replyTerm: 1, installReplyTermSet: true, installReplyTerm: 0}
		store := NewInmemSnapshotStore()
		sink, err := store.Create(3, 1, Configuration{}, 0)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		if _, err := sink.Write([]byte("snapshot data")); err != nil {
			t.Fatalf("Write failed: %v", err)
		}
		if err := sink.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		// prev = nextIndex-1 = 4 попадает в дыру между границей снимка (3)
		// и первой записью журнала (5) — отправляется снимок.
		cm, vf := newCM(mock, store, 3, 5, []LogEntry{{Index: 5, Term: 1}})

		cm.leaderSendAEsToPeer(1, 1, 0, true)

		if got := mock.installCallCount.Load(); got != 1 {
			t.Fatalf("InstallSnapshot calls = %d, want 1", got)
		}
		if got := mock.callCount.Load(); got != 0 {
			t.Fatalf("AppendEntries calls = %d, want 0 (snapshot path)", got)
		}
		if vf.votes != 1 {
			t.Fatalf("votes = %d, want 1 (snapshot path must not vote)", vf.votes)
		}
		if len(cm.leaderState.pendingVerify) != 1 || cm.leaderState.pendingVerify[0] != vf {
			t.Fatalf("pendingVerify = %v, want the request still queued", cm.leaderState.pendingVerify)
		}
	})
}
