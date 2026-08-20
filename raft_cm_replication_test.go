package raft

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// mockTransportAE — минимальная реализация Transport для тестов,
// подсчитывающая вызовы AppendEntries и InstallSnapshot с возможностью
// задания задержки AppendEntries.
type mockTransportAE struct {
	Transport
	callCount        atomic.Int32
	installCallCount atomic.Int32
	delay            time.Duration
	blockCh          chan struct{}

	// failReply — AppendEntries отвечает Success:false с заданным
	// конфликтом (ConflictIndex/ConflictTerm), вместо Success:true.
	failReply bool
	// failConflictIndex / failConflictTerm — поля конфликта для failReply.
	failConflictIndex int
	failConflictTerm  int
	// replyTerm — Term в ответах (0 по умолчанию: существующие тесты
	// полагаются на несовпадение с savedCurrentTerm, чтобы пропустить
	// ветки обработки ответа).
	replyTerm int
	// installReplyTerm — отдельный Term ответа InstallSnapshot; если
	// installReplyTermSet, используется вместо replyTerm (позволяет
	// независимо задавать термы ответов AE и IS).
	installReplyTerm    int
	installReplyTermSet bool
	// lastArgs — аргументы последнего AppendEntries (для утверждений
	// о содержимом запроса в тестах предиката).
	lastArgs    AppendEntriesArgs
	lastArgsSet bool
	// aeErr — транспортная ошибка AppendEntries: если задана, вызов
	// завершается ошибкой после учёта в callCount.
	aeErr error
}

func (m *mockTransportAE) AppendEntries(_ ServerID, args AppendEntriesArgs) (AppendEntriesReply, error) {
	m.callCount.Add(1)
	if m.aeErr != nil {
		return AppendEntriesReply{}, m.aeErr
	}
	m.lastArgs = args
	m.lastArgsSet = true
	if m.blockCh != nil {
		<-m.blockCh
	}
	if m.delay > 0 {
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(m.delay)
	}
	if m.failReply {
		return AppendEntriesReply{
			Term:          m.replyTerm,
			Success:       false,
			ConflictIndex: m.failConflictIndex,
			ConflictTerm:  m.failConflictTerm,
		}, nil
	}
	return AppendEntriesReply{
		Term:    m.replyTerm,
		Success: true,
	}, nil
}

func (m *mockTransportAE) InstallSnapshot(_ ServerID, _ InstallSnapshotRequest, _ io.Reader) (InstallSnapshotResponse, error) {
	m.installCallCount.Add(1)
	term := m.replyTerm
	if m.installReplyTermSet {
		term = m.installReplyTerm
	}
	return InstallSnapshotResponse{
		Success: true,
		Term:    term,
	}, nil
}

func (m *mockTransportAE) Consumer() <-chan RPC {
	return nil
}

func (m *mockTransportAE) RequestVote(_ ServerID, _ RequestVoteArgs) (RequestVoteReply, error) {
	return RequestVoteReply{}, nil
}

func (m *mockTransportAE) RequestPreVote(_ ServerID, _ RequestPreVoteArgs) (RequestPreVoteReply, error) {
	return RequestPreVoteReply{}, nil
}

func (m *mockTransportAE) TimeoutNow(_ ServerID, _ TimeoutNowRequest) (TimeoutNowResponse, error) {
	return TimeoutNowResponse{}, nil
}

func (m *mockTransportAE) AppendEntriesPipeline(_ ServerID) (AppendPipeline, error) {
	return nil, nil
}

func (m *mockTransportAE) Close() error {
	return nil
}

func (m *mockTransportAE) SetHeartbeatHandler(_ func(RPC)) {
}

func (m *mockTransportAE) LocalAddr() ServerAddress {
	return ""
}

func (m *mockTransportAE) EncodePeer(_ ServerID, _ ServerAddress) string {
	return ""
}

func (m *mockTransportAE) DecodePeer(_ string) ServerAddress {
	return ""
}

// TestLeaderSendAEs_Deduplication проверяет, что leaderSendAEs
// запускает не более одной горутины на peer.
//
// Сценарий:
//  1. ConsensusModule в состоянии Leader с inflightAE для peer 1.
//  2. Установить inflightAE[1] = true (имитация активной горутины).
//  3. Вызвать leaderSendAEs.
//  4. Проверить, что AppendEntries НЕ вызван (дедупликация сработала).
//  5. Сбросить inflightAE[1] = false.
//  6. Вызвать leaderSendAEs.
//  7. Проверить, что AppendEntries вызван (дедупликация разрешена).
func TestLeaderSendAEs_Deduplication(t *testing.T) {
	mock := &mockTransportAE{}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 0},
			matchIndex: map[int]int{1: -1},
			inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
		},
		cmState: cmState{
			state:        Leader,
			log:          make([]LogEntry, 0),
			lastLogIndex: -1,
			currentTerm:  1,
			commitIndex:  -1,
			configurations: configurations{
				committed: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
				latest: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
			},
		},

		id:        0,
		transport: mock,
	}
	cm.leaderState.inflightAE[1].Store(false)

	// Установить inflightAE[1] = true — имитация активной горутины.
	// Store — синхронная операция, отдельного ожидания не требуется.
	cm.leaderState.inflightAE[1].Store(true)

	// Вызвать leaderSendAEs — должен пропустить peer 1 (CAS=false).
	cm.leaderSendAEs()

	// Негативный assert — budgeted negative window (классификация).
	// Бюджет выведен из протокольной константы HeartbeatTimeoutMs:
	// за интервал heartbeat сломанная дедупликация успела бы выполнить
	// как минимум один AppendEntries.
	//
	// Инвариант окна: опрос НЕ доказывает отсутствия вызова — окно
	// осознанно временное и является единственным доступным способом
	// проверить «ничего не произошло». Увеличение окна не усиливает
	// assert, уменьшение — ослабляет его.
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	sleepMs(HeartbeatTimeoutMs)

	if calls := mock.callCount.Load(); calls > 0 {
		t.Errorf("expected 0 AppendEntries calls (dedup active), got %d", calls)
	}

	// Сбросить флаг — теперь leaderSendAEs должен запустить горутину.
	// Позитивный assert — poll по наблюдаемому условию (calls >= 1)
	// с дедлайном, а не фиксированная пауза.
	cm.leaderState.inflightAE[1].Store(false)
	cm.leaderSendAEs()

	deadline := time.Now().Add(inmemRPCTimeout)
	for mock.callCount.Load() == 0 {
		if !time.Now().Before(deadline) {
			t.Fatalf("expected AppendEntries call after inflightAE reset within %v, got 0",
				inmemRPCTimeout)
		}
		time.Sleep(pollInterval)
	}
}

// TestInflightAE_DeferReset проверяет, что leaderSendAEsToPeer
// сбрасывает inflightAE[peerID] через defer при любом завершении.
//
// Сценарий:
//  1. ConsensusModule в состоянии Leader, inflightAE[1] = true.
//  2. Вызвать leaderSendAEsToPeer (mock transport с успешным ответом).
//  3. После завершения проверить, что inflightAE[1] == false.
func TestInflightAE_DeferReset(t *testing.T) {
	mock := &mockTransportAE{}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 0},
			matchIndex: map[int]int{1: -1},
			inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
		},
		cmState: cmState{
			state:        Leader,
			log:          make([]LogEntry, 0),
			lastLogIndex: -1,
			currentTerm:  1,
			commitIndex:  -1,
			configurations: configurations{
				committed: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
				latest: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
			},
		},

		id:        0,
		transport: mock,
	}
	cm.leaderState.inflightAE[1].Store(false)

	// Установить флаг и вызвать leaderSendAEsToPeer.
	cm.leaderState.inflightAE[1].Store(true)
	cm.leaderSendAEsToPeer(1, 1, 0)
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(20 * time.Millisecond)

	// После завершения флаг должен быть сброшен.
	if cm.leaderState.inflightAE[1].Load() {
		t.Error("inflightAE[1] should be false after leaderSendAEsToPeer completes")
	}
}

// TestInflightAE_DeferResetOnSnapshotPath проверяет, что defer
// сбрасывает inflightAE при выходе через snapshot path.
//
// Сценарий:
//  1. ConsensusModule в состоянии Leader, snapshotStore != nil,
//     nextIndex[1] <= lastSnapshotIndex.
//  2. Установить inflightAE[1] = true.
//  3. Вызвать leaderSendAEsToPeer — она идёт по snapshot path.
//  4. После завершения inflightAE[1] == false.
func TestInflightAE_DeferResetOnSnapshotPath(t *testing.T) {
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:        map[int]int{1: -1},
			matchIndex:       map[int]int{1: -1},
			leaderStartIndex: -1,
			inflightAE:       map[int]*atomic.Bool{1: new(atomic.Bool)},
		},
		cmState: cmState{
			state:             Leader,
			log:               make([]LogEntry, 0),
			lastLogIndex:      -1,
			lastSnapshotIndex: -1,
			currentTerm:       1,
			commitIndex:       -1,
			votedFor:          -1,
			configurations: configurations{
				committed: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
				latest: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
			},
		},

		id:            0,
		transport:     &mockTransportAE{},
		storage:       NewMapStorage(),
		snapshotStore: &mockSnapshotStore{},
	}
	cm.leaderState.inflightAE[1].Store(false)

	cm.leaderState.inflightAE[1].Store(true)
	cm.leaderSendAEsToPeer(1, 1, 0)
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(20 * time.Millisecond)

	if cm.leaderState.inflightAE[1].Load() {
		t.Error("inflightAE[1] should be false after snapshot path exit")
	}
}

// TestLeaderSendAEs_StateCheck проверяет, что leaderSendAEs
// ничего не делает, если сервер не лидер.
//
// Сценарий:
//  1. ConsensusModule в состоянии Follower.
//  2. Вызвать leaderSendAEs.
//  3. Проверить, что AppendEntries не вызван.
func TestLeaderSendAEs_StateCheck(t *testing.T) {
	mock := &mockTransportAE{}
	cm := &ConsensusModule{
		leaderState: leaderState{
			inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
		},
		cmState: cmState{
			state: Follower,
		},

		id:        0,
		transport: mock,
	}

	cm.leaderSendAEs()
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(20 * time.Millisecond)

	if calls := mock.callCount.Load(); calls > 0 {
		t.Errorf("expected 0 AppendEntries calls for follower, got %d", calls)
	}
}

// TestInflightAE_NilSafe проверяет, что leaderSendAEs и
// leaderSendAEsToPeer безопасны при nil в inflightAE.
//
// Сценарий:
//  1. ConsensusModule с inflightAE = nil.
//  2. Вызвать leaderSendAEs — не должно быть panic.
//  3. Вызвать leaderSendAEsToPeer — не должно быть panic.
func TestInflightAE_NilSafe(t *testing.T) {
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 0},
			matchIndex: map[int]int{1: -1},
			inflightAE: nil,
		},
		cmState: cmState{
			state:        Leader,
			log:          make([]LogEntry, 0),
			lastLogIndex: -1,
			currentTerm:  1,
			commitIndex:  -1,
		},

		id:        0,
		transport: &mockTransportAE{},
	}

	// Не должно быть panic.
	cm.leaderSendAEs()
	cm.leaderSendAEsToPeer(1, 1, 0)
}

// TestBecomeFollower_InflightAE_NilEntry проверяет, что becomeFollowerLocked
// безопасна при наличии nil-элемента в inflightAE. Тот же защищённый
// код используется и в defer блока runLeaderLoop при выходе из цикла
// лидера, поэтому тест покрывает оба места сброса флагов.
//
// Сценарий:
//  1. ConsensusModule в состоянии Leader, inflightAE = {1: nil, 2: true}.
//     Элемент 1 (nil) имитирует peer, добавленного в конфигурацию, но
//     не инициализированного через startStopReplicationLocked до момента
//     завершения лидерства (реконфигурация не успела обработаться).
//  2. Вызвать becomeFollowerLocked(1) — не должно быть panic: ранее вызов
//     Store(false) на nil *atomic.Bool давал nil dereference.
//  3. Проверить, что inflightAE[2] сброшен в false, а nil-элемент
//     пропущен без паники.
func TestBecomeFollower_InflightAE_NilEntry(t *testing.T) {
	cm := &ConsensusModule{
		leaderState: leaderState{
			inflightAE: map[int]*atomic.Bool{
				1: nil,              // peer вне конфигурации — элемент не инициализирован
				2: new(atomic.Bool), // активный peer, флаг установлен
			},
		},
		cmState: cmState{
			state:             Leader,
			electionTimerDone: make(chan struct{}),
			currentTerm:       1,
		},

		id:         0,
		stepDown:   make(chan struct{}, 1),
		shutdownCh: make(chan struct{}),
	}
	cm.leaderState.inflightAE[2].Store(true)

	// becomeFollowerLocked требует удержания cm.mu.
	cm.mu.Lock()
	cm.becomeFollowerLocked(1)
	cm.mu.Unlock()

	if cm.cmState.state != Follower {
		t.Errorf("state = %v, want Follower", cm.cmState.state)
	}
	if cm.leaderState.inflightAE[2].Load() {
		t.Error("inflightAE[2] should be reset to false")
	}
}

// TestInflightAE_CASConcurrent проверяет, что atomic CAS
// не вызывает data race при конкурентном доступе.
//
// Сценарий:
//  1. Создать inflightAE с одним peer.
//  2. Запустить 10 горутин, каждая делает CAS(false→true) и Store(false).
//  3. Проверить, что нет panic и все операции завершаются.
//
// Запуск: только с -race флагом.
func TestInflightAE_CASConcurrent(t *testing.T) {
	flag := &atomic.Bool{}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if flag.CompareAndSwap(false, true) {
				flag.Store(false)
			}
		}()
	}
	wg.Wait()
}

// mockSnapshotStore — минимальная реализация SnapshotStore для тестов.
type mockSnapshotStore struct {
	SnapshotStore
}

func (m *mockSnapshotStore) List() ([]*SnapshotMeta, error) {
	return []*SnapshotMeta{
		{Index: 0, Term: 1, Size: 0},
	}, nil
}

func (m *mockSnapshotStore) Open(_ string) (*SnapshotMeta, io.ReadCloser, error) {
	return &SnapshotMeta{Index: 0, Term: 1, Size: 0}, io.NopCloser(bytes.NewReader(nil)), nil
}

// mockSnapshotStoreCfg — конфигурируемая реализация SnapshotStore для
// тестов leaderSendSnapshot. Позволяет имитировать повреждённое хранилище:
// nil-элемент в List(), пустой ID, ошибку List() и т.д.
type mockSnapshotStoreCfg struct {
	SnapshotStore
	listResult []*SnapshotMeta // результат List(); nil означает «снимков нет»
	listErr    error           // ошибка, возвращаемая List()
	openMeta   *SnapshotMeta   // метаданные, возвращаемые Open()
}

// List возвращает предзаданный список снимков или ошибку.
func (m *mockSnapshotStoreCfg) List() ([]*SnapshotMeta, error) {
	return m.listResult, m.listErr
}

// Open возвращает предзаданные метаданные и пустой ридер — для проверки
// корректности вызова важны лишь метаданные, содержимое не читается.
func (m *mockSnapshotStoreCfg) Open(_ string) (*SnapshotMeta, io.ReadCloser, error) {
	return m.openMeta, io.NopCloser(bytes.NewReader(nil)), nil
}

// TestLeaderSendSnapshot_NilSnapshotStore проверяет, что leaderSendSnapshot
// безопасна при nil-хранилище снимков.
//
// Сценарий:
//  1. ConsensusModule в состоянии Leader, snapshotStore == nil.
//  2. Вызвать leaderSendSnapshot — не должно быть panic.
//  3. Проверить, что InstallSnapshot НЕ вызван.
//
// Функция выполняется в отдельной горутине (через leaderSendAEsToPeer),
// поэтому паника здесь без recover() убила бы весь процесс.
func TestLeaderSendSnapshot_NilSnapshotStore(t *testing.T) {
	transport := &mockTransportAE{}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: -1},
			matchIndex: map[int]int{1: -1},
		},
		cmState: cmState{
			state:       Leader,
			currentTerm: 1,
		},

		id:            0,
		transport:     transport,
		snapshotStore: nil,
	}

	// Никаких обращений к хранилищу не должно быть.
	cm.leaderSendSnapshot(1, 1)

	if n := transport.installCallCount.Load(); n != 0 {
		t.Errorf("expected 0 InstallSnapshot calls, got %d", n)
	}
}

// TestLeaderSendSnapshot_NilMetaInList проверяет, что leaderSendSnapshot
// безопасна, когда List() возвращает слайс с nil-элементом.
//
// Сценарий:
//  1. snapshotStore.List() возвращает []*SnapshotMeta{nil}.
//  2. Вызвать leaderSendSnapshot — не должно быть panic, иначе обращение
//     snapshots[0].ID привело бы к nil dereference.
//  3. Проверить, что InstallSnapshot НЕ вызван.
func TestLeaderSendSnapshot_NilMetaInList(t *testing.T) {
	transport := &mockTransportAE{}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: -1},
			matchIndex: map[int]int{1: -1},
		},
		cmState: cmState{
			state:       Leader,
			currentTerm: 1,
		},

		id:        0,
		transport: transport,
		snapshotStore: &mockSnapshotStoreCfg{
			listResult: []*SnapshotMeta{nil},
		},
	}

	cm.leaderSendSnapshot(1, 1)

	if n := transport.installCallCount.Load(); n != 0 {
		t.Errorf("expected 0 InstallSnapshot calls, got %d", n)
	}
}

// TestLeaderSendSnapshot_EmptyID проверяет, что leaderSendSnapshot
// безопасна, когда snapshots[0].ID пуст.
//
// Сценарий:
//  1. snapshotStore.List() возвращает снимок с пустым ID.
//  2. Вызвать leaderSendSnapshot — не должно быть вызова snapshotStore.Open
//     с некорректным ID (реализации хранилища могут паниковать на нём).
//  3. Проверить, что InstallSnapshot НЕ вызван.
func TestLeaderSendSnapshot_EmptyID(t *testing.T) {
	transport := &mockTransportAE{}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: -1},
			matchIndex: map[int]int{1: -1},
		},
		cmState: cmState{
			state:       Leader,
			currentTerm: 1,
		},

		id:        0,
		transport: transport,
		snapshotStore: &mockSnapshotStoreCfg{
			listResult: []*SnapshotMeta{{ID: ""}},
		},
	}

	cm.leaderSendSnapshot(1, 1)

	if n := transport.installCallCount.Load(); n != 0 {
		t.Errorf("expected 0 InstallSnapshot calls, got %d", n)
	}
}

// TestLeaderSendSnapshot_Success проверяет, что leaderSendSnapshot при
// корректном хранилище отправляет InstallSnapshot и обновляет nextIndex
// и matchIndex, а также уведомляет commitmentTracker
// (симметрия с путём AE).
//
// Сценарий:
//  1. snapshotStore.List() возвращает один снимок (Index=5, Term=2, Size=100).
//  2. transport.InstallSnapshot возвращает Success=true, Term=1 — терм
//     follower'а совпадает с термом запроса (реалистичный ответ).
//  3. Проверить, что InstallSnapshot вызван ровно один раз.
//  4. Проверить, что nextIndex[1] == 6, matchIndex[1] == 5 и
//     commitmentTracker.matchIndex[1] == 5.
func TestLeaderSendSnapshot_Success(t *testing.T) {
	transport := &mockTransportAE{replyTerm: 1}
	tracker := newCommitmentTracker(0, 1, -1, make(chan int, 1))
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:         map[int]int{1: -1},
			matchIndex:        map[int]int{1: -1},
			commitmentTracker: tracker,
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastSnapshotIndex: 5,
			lastSnapshotTerm:  2,
		},

		id:        0,
		transport: transport,
		snapshotStore: &mockSnapshotStoreCfg{
			listResult: []*SnapshotMeta{{ID: "snap-5", Index: 5, Term: 2, Size: 100}},
			openMeta:   &SnapshotMeta{Index: 5, Term: 2, Size: 100},
		},
	}
	tracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)

	cm.leaderSendSnapshot(1, 1)

	if n := transport.installCallCount.Load(); n != 1 {
		t.Fatalf("expected 1 InstallSnapshot call, got %d", n)
	}
	if cm.leaderState.nextIndex[1] != 6 {
		t.Errorf("nextIndex[1] = %d, want 6", cm.leaderState.nextIndex[1])
	}
	if cm.leaderState.matchIndex[1] != 5 {
		t.Errorf("matchIndex[1] = %d, want 5", cm.leaderState.matchIndex[1])
	}
	if got := tracker.matchIndex[1]; got != 5 {
		t.Errorf("commitmentTracker.matchIndex[1] = %d, want 5 (setMatch on IS path)", got)
	}
}

// TestLeaderSendSnapshot_SuccessStaleTerm проверяет защиту от
// запоздалого ответа из вытесненного term'а: при
// reply.Term != term запроса (ответ из прошлого лидерства) состояние
// репликации не изменяется.
func TestLeaderSendSnapshot_SuccessStaleTerm(t *testing.T) {
	transport := &mockTransportAE{replyTerm: 1}
	tracker := newCommitmentTracker(0, 2, -1, make(chan int, 1))
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:         map[int]int{1: 7},
			matchIndex:        map[int]int{1: 3},
			commitmentTracker: tracker,
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       2,
			lastSnapshotIndex: 5,
			lastSnapshotTerm:  1,
		},

		id:        0,
		transport: transport,
		snapshotStore: &mockSnapshotStoreCfg{
			listResult: []*SnapshotMeta{{ID: "snap-5", Index: 5, Term: 1, Size: 100}},
			openMeta:   &SnapshotMeta{Index: 5, Term: 1, Size: 100},
		},
	}
	tracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)

	// term запроса 2, reply.Term 1 — ответ устарел.
	cm.leaderSendSnapshot(1, 2)

	if cm.leaderState.nextIndex[1] != 7 {
		t.Errorf("nextIndex[1] = %d, want unchanged (7)", cm.leaderState.nextIndex[1])
	}
	if cm.leaderState.matchIndex[1] != 3 {
		t.Errorf("matchIndex[1] = %d, want unchanged (3)", cm.leaderState.matchIndex[1])
	}
	if got := tracker.matchIndex[1]; got != -1 {
		t.Errorf("commitmentTracker.matchIndex[1] = %d, want -1 (not updated)", got)
	}
}

// TestLeaderSendSnapshot_SuccessNotLeader проверяет защиту от
// запоздалого ответа после потери лидерства: при
// state != Leader состояние репликации не изменяется.
func TestLeaderSendSnapshot_SuccessNotLeader(t *testing.T) {
	transport := &mockTransportAE{replyTerm: 1}
	tracker := newCommitmentTracker(0, 1, -1, make(chan int, 1))
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:         map[int]int{1: 7},
			matchIndex:        map[int]int{1: 3},
			commitmentTracker: tracker,
		},
		cmState: cmState{
			state:             Follower, // лидерство уже потеряно
			currentTerm:       1,
			lastSnapshotIndex: 5,
			lastSnapshotTerm:  1,
		},

		id:        0,
		transport: transport,
		snapshotStore: &mockSnapshotStoreCfg{
			listResult: []*SnapshotMeta{{ID: "snap-5", Index: 5, Term: 1, Size: 100}},
			openMeta:   &SnapshotMeta{Index: 5, Term: 1, Size: 100},
		},
	}
	tracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)

	cm.leaderSendSnapshot(1, 1)

	if cm.leaderState.nextIndex[1] != 7 || cm.leaderState.matchIndex[1] != 3 {
		t.Errorf("replication state changed after leadership loss: nextIndex=%d matchIndex=%d",
			cm.leaderState.nextIndex[1], cm.leaderState.matchIndex[1])
	}
	if got := tracker.matchIndex[1]; got != -1 {
		t.Errorf("commitmentTracker.matchIndex[1] = %d, want -1 (not updated)", got)
	}
}

// newRejectionTestCM собирает литеральный CM-лидер для тестов обработки
// отказа AppendEntries: nextIndex[1]=10, matchIndex[1]=5, журнал с
// записями 9..10. Транспорт — mock с настраиваемым отказом.
func newRejectionTestCM(transport *mockTransportAE) *ConsensusModule {
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 10},
			matchIndex: map[int]int{1: 5},
			inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastLogIndex:      10,
			lastLogTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			commitIndex:       5,
			termIndexMap:      map[int]int{1: 10},
			log: []LogEntry{
				{Index: 9, Term: 1},
				{Index: 10, Term: 1},
			},
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
				latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
			},
		},
		id:        0,
		transport: transport,
	}
	cm.leaderState.inflightAE[1].Store(false)
	return cm
}

// TestLeaderSendAEsToPeer_RejectsImpossibleConflictIndex — тест 2:
// mock-ответ Success:false ConflictIndex:0 ConflictTerm:-1 при известном
// matchIndex[1]=5 (N > 0) — отказ невозможен, nextIndex не изменяется,
// счётчик предохранителя увеличивается, InstallSnapshot не отправляется.
// На HEAD до nextIndex стал бы 0 — тест падает.
func TestLeaderSendAEsToPeer_RejectsImpossibleConflictIndex(t *testing.T) {
	mock := &mockTransportAE{failReply: true, failConflictIndex: 0, failConflictTerm: -1, replyTerm: 1}
	cm := newRejectionTestCM(mock)

	cm.leaderSendAEsToPeer(1, 1, 0)

	if cm.leaderState.nextIndex[1] != 10 {
		t.Fatalf("nextIndex[1] = %d, want unchanged (10) — impossible rejection must be ignored",
			cm.leaderState.nextIndex[1])
	}
	if got := cm.counters.nextIndexRejectionIgnored[1]; got != 1 {
		t.Fatalf("nextIndexRejectionIgnored[1] = %d, want 1", got)
	}
	if got := cm.counters.appendEntriesRejected[1]; got != 1 {
		t.Fatalf("appendEntriesRejected[1] = %d, want 1", got)
	}
	if n := mock.installCallCount.Load(); n != 0 {
		t.Fatalf("InstallSnapshot sent %d times after rejected failure, want 0", n)
	}
}

// TestLeaderSendAEsToPeer_AcceptsPlausibleRejection — контрольный случай
// предохранителя: корректный отказ (ConflictIndex выше
// matchIndex) НЕ отбраковывается — nextIndex обновляется штатно,
// счётчик предохранителя остаётся нулевым.
func TestLeaderSendAEsToPeer_AcceptsPlausibleRejection(t *testing.T) {
	mock := &mockTransportAE{failReply: true, failConflictIndex: 8, failConflictTerm: -1, replyTerm: 1}
	cm := newRejectionTestCM(mock)

	cm.leaderSendAEsToPeer(1, 1, 0)

	if cm.leaderState.nextIndex[1] != 8 {
		t.Fatalf("nextIndex[1] = %d, want 8 (plausible rejection applied)", cm.leaderState.nextIndex[1])
	}
	if got := cm.counters.nextIndexRejectionIgnored[1]; got != 0 {
		t.Fatalf("nextIndexRejectionIgnored[1] = %d, want 0", got)
	}
}

// TestLeaderSendAEsToPeer_SnapshotPredicateBoundaries — граничные случаи
// предиката «снимок или AE» (тест 3,):
//   - prev == lastSnapshotIndex → AE (lookupTermLocked даёт lastSnapshotTerm
//     по fallback);
//   - prev == first → AE (запись физически в срезе);
//   - prev == first-1 при first-1 != lastSnapshotIndex → снимок.
func TestLeaderSendAEsToPeer_SnapshotPredicateBoundaries(t *testing.T) {
	tests := []struct {
		name              string
		lastSnapshotIndex int
		log               []LogEntry
		nextIndex         int
		wantInstall       bool
		wantPrevLogTerm   int
	}{
		{
			name:              "prev == lastSnapshotIndex → AE",
			lastSnapshotIndex: 5,
			log:               []LogEntry{{Index: 6, Term: 1}, {Index: 7, Term: 1}},
			nextIndex:         6,
			wantInstall:       false,
			wantPrevLogTerm:   2, // fallback на lastSnapshotTerm
		},
		{
			name:              "prev == first → AE",
			lastSnapshotIndex: 5,
			log:               []LogEntry{{Index: 6, Term: 1}, {Index: 7, Term: 1}},
			nextIndex:         7,
			wantInstall:       false,
			wantPrevLogTerm:   1, // запись 6 в срезе
		},
		{
			name:              "prev == first-1 вне границы снапшота → InstallSnapshot",
			lastSnapshotIndex: 3,
			log:               []LogEntry{{Index: 5, Term: 1}},
			nextIndex:         5,
			wantInstall:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// replyTerm=1 (ответы AE обрабатываются); installReplyTerm=0 —
			// ответ InstallSnapshot не совпадает с термом запроса, ветка
			// обновления состояния репликации (и setMatch в
			// commitmentTracker) пропускается — литеральный CM без трекера
			// остаётся безопасным; проверяется только ветвление
			// «снимок или AE».
			mock := &mockTransportAE{
				failReply: true, failConflictIndex: 0, failConflictTerm: -1,
				replyTerm: 1, installReplyTermSet: true, installReplyTerm: 0,
			}
			cm := &ConsensusModule{
				leaderState: leaderState{
					nextIndex:  map[int]int{1: tt.nextIndex},
					matchIndex: map[int]int{1: -1},
					inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
				},
				cmState: cmState{
					state:             Leader,
					currentTerm:       1,
					lastLogIndex:      7,
					lastLogTerm:       1,
					lastSnapshotIndex: tt.lastSnapshotIndex,
					lastSnapshotTerm:  2,
					commitIndex:       -1,
					termIndexMap:      make(map[int]int),
					log:               tt.log,
					configurations: configurations{
						committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
						latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
					},
				},
				id:        0,
				transport: mock,
				snapshotStore: &mockSnapshotStoreCfg{
					listResult: []*SnapshotMeta{{ID: "snap", Index: tt.lastSnapshotIndex, Term: 2, Size: 0}},
					openMeta:   &SnapshotMeta{ID: "snap", Index: tt.lastSnapshotIndex, Term: 2, Size: 0},
				},
			}
			cm.leaderState.inflightAE[1].Store(false)

			cm.leaderSendAEsToPeer(1, 1, 0)

			installs := mock.installCallCount.Load()
			calls := mock.callCount.Load()
			if tt.wantInstall {
				if installs != 1 || calls != 0 {
					t.Fatalf("want InstallSnapshot: installs=%d calls=%d", installs, calls)
				}
				return
			}
			if calls != 1 || installs != 0 {
				t.Fatalf("want AppendEntries: installs=%d calls=%d", installs, calls)
			}
			if mock.lastArgs.PrevLogTerm != tt.wantPrevLogTerm {
				t.Fatalf("PrevLogTerm = %d, want %d", mock.lastArgs.PrevLogTerm, tt.wantPrevLogTerm)
			}
		})
	}
}

// TestReplicationBackoffDelay_Clamp — проверка формулы задержки
// delay = min(HeartbeatTimeoutMs·2^min(f,5),
// 1000) мс; при f >= 5 задержка не превышает потолок 1000 мс
// (33·2⁵ = 1056 без ограничения диапазоном).
func TestReplicationBackoffDelay_Clamp(t *testing.T) {
	if got := replicationBackoffDelay(0); got != 0 {
		t.Fatalf("delay(0) = %v, want 0 (no backoff without failures)", got)
	}
	if got := replicationBackoffDelay(1); got != 66*time.Millisecond {
		t.Fatalf("delay(1) = %v, want 66ms (33·2)", got)
	}
	if got := replicationBackoffDelay(5); got != 1000*time.Millisecond {
		t.Fatalf("delay(5) = %v, want 1000ms (clamped ceiling)", got)
	}
	if got := replicationBackoffDelay(50); got != 1000*time.Millisecond {
		t.Fatalf("delay(50) = %v, want 1000ms (clamped ceiling)", got)
	}
}

// TestReplicationBackoff_LogicalRejectionsDoNotBackoff — тест 3:
// пир доступен, но отвечает Success:false → задержка повторов не активируется,
// частота попыток прежняя (каждый вызов доходит до транспорта),
// replFailures остаётся нулевым.
func TestReplicationBackoff_LogicalRejectionsDoNotBackoff(t *testing.T) {
	mock := &mockTransportAE{failReply: true, failConflictIndex: 0, failConflictTerm: -1, replyTerm: 1}
	cm := &ConsensusModule{
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
				committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
				latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
			},
		},
		id:        0,
		transport: mock,
	}
	cm.leaderState.inflightAE[1].Store(false)

	const attempts = 5
	for i := 0; i < attempts; i++ {
		cm.leaderSendAEsToPeer(1, 1, 0)
	}

	if got := mock.callCount.Load(); got != attempts {
		t.Fatalf("transport calls = %d, want %d — logical rejections must not trigger backoff", got, attempts)
	}
	cm.mu.Lock()
	rf := cm.leaderState.replFailures[1]
	cm.mu.Unlock()
	if rf != 0 {
		t.Fatalf("replFailures[1] = %d, want 0 (logical rejection is not a transport error)", rf)
	}
}

// TestReplicationBackoff_LeaderStateReinitialized — тест 4:
// жизненный цикл полей задержки повторов строго следует роли Leader: startLeaderLocked
// пересоздаёт карты replFailures/lastAttempt (утечки записей прошлого
// лидерства исключены).
func TestReplicationBackoff_LeaderStateReinitialized(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.storage = NewMapStorage()
	cm.shutdownCh = make(chan struct{})
	cm.commitCh = make(chan int, 1)
	cm.stepDown = make(chan struct{}, 1)
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastLogIndex = -1
	cm.cmState.lastLogTerm = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	cm.cmState.termIndexMap = make(map[int]int)
	cm.cmState.configurations = configurations{
		committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}}},
		latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}}},
	}
	cm.leaderState.nextIndex = make(map[int]int)
	cm.leaderState.matchIndex = make(map[int]int)
	cm.leaderState.inflightAE = make(map[int]*atomic.Bool)
	cm.leaderState.inflight = make(map[int]*logFuture)
	defer cm.Stop()

	// Загрязнённое состояние прошлого лидерства.
	cm.leaderState.replFailures = map[int]int{1: 7}
	cm.leaderState.lastAttempt = map[int]time.Time{1: time.Now()}

	cm.mu.Lock()
	cm.startLeaderLocked()
	replLen := len(cm.leaderState.replFailures)
	attemptLen := len(cm.leaderState.lastAttempt)
	cm.mu.Unlock()

	if replLen != 0 || attemptLen != 0 {
		t.Fatalf("backoff state not reinitialized on startLeaderLocked: replFailures=%d entries, lastAttempt=%d entries",
			replLen, attemptLen)
	}
}

// TestReplicationBackoff_SkipsTransportAndDoesNotRecordAttempt — при активной
// задержке повторов попытка репликации на соседа пропускается: транспорт не
// вызывается, а момент последней попытки не обновляется.
//
// Защищаемый инвариант: задержка повторов вычисляется сравнением текущего
// времени с моментом последней реальной попытки. Если бы момент попытки
// обновлялся и на пути пропуска, каждый пульс сдвигал бы его вперёд, условие
// пропуска стало бы вечно истинным, и сосед не получал бы ни AppendEntries,
// ни снимок до смены лидера.
func TestReplicationBackoff_SkipsTransportAndDoesNotRecordAttempt(t *testing.T) {
	mock := &mockTransportAE{}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 1},
			matchIndex: map[int]int{1: 0},
			inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
			// Одна транспортная ошибка при HeartbeatTimeoutMs = 33 даёт
			// задержку 66 мс, поэтому только что зафиксированная попытка
			// гарантирует активную задержку.
			replFailures: map[int]int{1: 1},
			lastAttempt:  map[int]time.Time{1: time.Now()},
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
				committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
				latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
			},
		},
		id:        0,
		transport: mock,
	}
	cm.leaderState.inflightAE[1].Store(false)
	lastAttemptBefore := cm.leaderState.lastAttempt[1]

	cm.leaderSendAEsToPeer(1, 1, 0)

	if got := mock.callCount.Load(); got != 0 {
		t.Fatalf("AppendEntries calls = %d, want 0 (backoff must skip the attempt)", got)
	}
	if got := mock.installCallCount.Load(); got != 0 {
		t.Fatalf("InstallSnapshot calls = %d, want 0 (backoff must skip the attempt)", got)
	}
	if got := cm.leaderState.lastAttempt[1]; !got.Equal(lastAttemptBefore) {
		t.Fatalf("lastAttempt[1] = %v, want unchanged %v (skipped attempt must not be recorded)",
			got, lastAttemptBefore)
	}
	if got := cm.leaderState.replFailures[1]; got != 1 {
		t.Fatalf("replFailures[1] = %d, want unchanged (1)", got)
	}
	if cm.leaderState.inflightAE[1].Load() {
		t.Fatal("inflightAE[1] = true, want false (flag must be cleared on every exit)")
	}
}

// TestLeaderSendAEsToPeer_HigherTermStepsDown — ответ соседа с бо́льшим термом
// переводит лидера в ведомого: терм обновляется, голос сбрасывается,
// состояние репликации не изменяется.
//
// becomeFollowerLocked вызывается под удерживаемой cm.mu: внутри она порождает
// горутину таймера выборов, а контракт порождения требует, чтобы проверка
// «узел не остановлен» и регистрация горутины выполнялись под одной
// блокировкой. Поэтому тест держит открытыми каналы завершения и завершает
// порождённую горутину через Stop().
func TestLeaderSendAEsToPeer_HigherTermStepsDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	mock := &mockTransportAE{replyTerm: 5}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 1},
			matchIndex: map[int]int{1: 0},
			inflightAE: map[int]*atomic.Bool{1: new(atomic.Bool)},
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			votedFor:          0,
			lastLogIndex:      0,
			lastLogTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			commitIndex:       -1,
			termIndexMap:      map[int]int{1: 0},
			log:               []LogEntry{{Index: 0, Term: 1}},
			// Таймер выборов останавливается закрытием этого канала,
			// becomeFollowerLocked закрывает текущий и создаёт новый.
			electionTimerDone: make(chan struct{}),
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
				latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
			},
		},
		id:         0,
		transport:  mock,
		storage:    NewMapStorage(),
		stepDown:   make(chan struct{}, 1),
		shutdownCh: make(chan struct{}),
	}
	cm.leaderState.inflightAE[1].Store(false)
	defer cm.Stop()

	cm.leaderSendAEsToPeer(1, 1, 0)

	// Чтение состояния под cm.mu: горутина таймера выборов уже запущена.
	cm.mu.Lock()
	state := cm.cmState.state
	currentTerm := cm.cmState.currentTerm
	votedFor := cm.cmState.votedFor
	nextIndex := cm.leaderState.nextIndex[1]
	matchIndex := cm.leaderState.matchIndex[1]
	cm.mu.Unlock()

	if state != Follower {
		t.Fatalf("state = %v, want Follower (higher term in reply must step the leader down)", state)
	}
	if currentTerm != 5 {
		t.Fatalf("currentTerm = %d, want 5", currentTerm)
	}
	if votedFor != -1 {
		t.Fatalf("votedFor = %d, want -1", votedFor)
	}
	if nextIndex != 1 || matchIndex != 0 {
		t.Fatalf("replication state changed on step down: nextIndex=%d matchIndex=%d, want 1 and 0",
			nextIndex, matchIndex)
	}
	if cm.leaderState.inflightAE[1].Load() {
		t.Fatal("inflightAE[1] = true, want false (flag must be cleared on every exit)")
	}
}

// TestReplicationBackoff_TransportErrorIncrementsFailures — зеркальный случай
// к отказу ведомого: транспортная ошибка включает задержку повторов, тогда как
// логический отказ (Success: false) её не включает.
//
// Момент попытки фиксируется до обращения к транспорту, поэтому он заполнен
// даже при ошибке; состояние репликации при этом не изменяется, а счётчик
// отказов AppendEntries не растёт — транспортная ошибка отказом ведомого
// не является.
func TestReplicationBackoff_TransportErrorIncrementsFailures(t *testing.T) {
	mock := &mockTransportAE{aeErr: errors.New("transport unavailable")}
	cm := newRejectionTestCM(mock)

	cm.leaderSendAEsToPeer(1, 1, 0)

	if got := mock.callCount.Load(); got != 1 {
		t.Fatalf("AppendEntries calls = %d, want 1", got)
	}
	if got := cm.leaderState.replFailures[1]; got != 1 {
		t.Fatalf("replFailures[1] = %d, want 1 (transport error enables backoff)", got)
	}
	if cm.leaderState.nextIndex[1] != 10 || cm.leaderState.matchIndex[1] != 5 {
		t.Fatalf("replication state changed on transport error: nextIndex=%d matchIndex=%d, want 10 and 5",
			cm.leaderState.nextIndex[1], cm.leaderState.matchIndex[1])
	}
	if cm.leaderState.lastAttempt[1].IsZero() {
		t.Fatal("lastAttempt[1] is zero, want recorded before the RPC")
	}
	if got := cm.counters.appendEntriesRejected[1]; got != 0 {
		t.Fatalf("appendEntriesRejected[1] = %d, want 0 (transport error is not a logical rejection)", got)
	}
	if cm.leaderState.inflightAE[1].Load() {
		t.Fatal("inflightAE[1] = true, want false (flag must be cleared on every exit)")
	}
}

// TestLeaderSendAEsToPeer_StaleReplyDoesNotMutateState — запоздавший ответ не
// изменяет состояние репликации.
//
// Ответ горутины вытесненного лидерства (терм ответа не совпадает с термом
// отправки) и ответ, пришедший после утраты лидерства, обязаны быть
// отброшены: иначе nextIndex и matchIndex нового лидера откатились бы к
// значениям прошлого терма.
func TestLeaderSendAEsToPeer_StaleReplyDoesNotMutateState(t *testing.T) {
	tests := []struct {
		name      string
		state     CMState
		replyTerm int
	}{
		{
			name:      "терм ответа не совпадает с термом отправки",
			state:     Leader,
			replyTerm: 0,
		},
		{
			name:      "узел больше не лидер",
			state:     Follower,
			replyTerm: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockTransportAE{replyTerm: tt.replyTerm}
			cm := newRejectionTestCM(mock)
			cm.cmState.state = tt.state
			cm.leaderState.replFailures = map[int]int{1: 2}

			cm.leaderSendAEsToPeer(1, 1, 0)

			if cm.leaderState.nextIndex[1] != 10 || cm.leaderState.matchIndex[1] != 5 {
				t.Fatalf("stale reply mutated replication state: nextIndex=%d matchIndex=%d, want 10 and 5",
					cm.leaderState.nextIndex[1], cm.leaderState.matchIndex[1])
			}
			if tt.state != Leader && cm.leaderState.replFailures[1] != 2 {
				t.Fatalf("replFailures[1] = %d, want unchanged (2) — node is no longer the leader",
					cm.leaderState.replFailures[1])
			}
			if cm.leaderState.inflightAE[1].Load() {
				t.Fatal("inflightAE[1] = true, want false (flag must be cleared on every exit)")
			}
		})
	}
}
