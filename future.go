package raft

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrNotLeader                    = errors.New("raft: not leader")
	ErrLeadershipLost               = errors.New("raft: leadership lost while committing")
	ErrRaftShutdown                 = errors.New("raft: raft is shutdown")
	ErrEnqueueTimeout               = errors.New("raft: timeout enqueuing operation")
	ErrUnsupportedProtocol          = errors.New("raft: unsupported protocol version")
	ErrLeadershipTransferInProgress = errors.New("raft: leadership transfer in progress")

	// ErrNothingNewToSnapshot возвращается, когда нет новых зафиксированных
	// записей для создания снимка.
	ErrNothingNewToSnapshot = errors.New("raft: nothing new to snapshot")

	// ErrBatchFSMResponseMismatch — ошибка несоответствия количества ответов от FSM.
	// BatchingFSM требует, чтобы ApplyBatch возвращал ровно столько ответов,
	// сколько записей журнала было в батче. Ошибка передаётся клиенту через
	// ApplyFuture.Error(); горутина runFSM при этом не завершается.
	ErrBatchFSMResponseMismatch = errors.New("raft: ApplyBatch response count mismatch")
)

// Future представляет асинхронную операцию Raft.
type Future interface {
	Error() error
	// ErrorCh возвращает канал, по которому приходит ошибка выполнения.
	// Позволяет использовать select для ожидания без создания го-рутины.
	ErrorCh() <-chan error
}

// verifyFuture — Future для подтверждения ReadIndex (Raft §8).
// Помещается в verifyCh и обрабатывается в runLeaderLoop.
// При получении кворумного подтверждения от большинства голосующих
// вызывается respond(nil). Если лидер утратил лидерство — respond(ErrNotLeader).
//
// Поля votes, voted, quorumSize и epoch не требуют отдельного мьютекса:
// оба места вызова vote() (обработчик verifyCh в runLeaderLoop и ветка
// успешного ответа leaderSendAEsToPeer) выполняются под блокировкой cm.mu
// ConsensusModule, что гарантирует потокобезопасность.
type verifyFuture struct {
	deferError

	quorumSize int
	votes      int

	// voted — множество peerID, от которых голос уже учтён.
	// Обеспечивает дедупликацию: повторные подтверждения пульса одного
	// узла не накапливаются.
	voted map[int]struct{}

	// epoch — раунд верификации, к которому привязан запрос.
	// Присваивается в leaderLoop при обработке запроса; голос
	// засчитывается только от AE, отправленного с dispatchEpoch >= epoch
	// (AE отправлен не раньше запроса, Raft §8).
	epoch uint64
}

// vote регистрирует голос узла peerID при подтверждении лидерства.
// Если лидерство не подтверждено (leader == false), future завершается
// с ErrNotLeader.
// Повторный голос от того же peerID игнорируется (дедупликация).
// При достижении кворума (votes >= quorumSize) future завершается
// успешно (nil).
// Метод потокобезопасен, если все вызовы выполняются под блокировкой cm.mu.
func (v *verifyFuture) vote(peerID int, leader bool) {
	if !leader {
		v.respond(ErrNotLeader)
		return
	}
	if _, ok := v.voted[peerID]; ok {
		return
	}
	v.voted[peerID] = struct{}{}
	v.votes++
	if v.votes >= v.quorumSize {
		v.respond(nil)
	}
}

// IndexFuture — Future, возвращающая индекс записи в журнале.
type IndexFuture interface {
	Future
	Index() int
}

// ApplyFuture — Future для Apply-операции.
type ApplyFuture interface {
	IndexFuture
	Response() any
}

// ConfigurationFuture — Future, возвращающая конфигурацию кластера.
type ConfigurationFuture interface {
	IndexFuture
	Configuration() Configuration
}

// deferError — базовая реализация Future с каналом завершения.
type deferError struct {
	err        error
	errCh      chan error
	once       sync.Once
	responded  bool
	mu         sync.Mutex
	shutdownCh chan struct{}
}

func (d *deferError) init(shutdownCh chan struct{}) {
	d.errCh = make(chan error, 1)
	d.shutdownCh = shutdownCh
}

func (d *deferError) Error() error {
	d.once.Do(func() {
		select {
		case d.err = <-d.errCh:
		case <-d.shutdownCh:
			d.err = ErrRaftShutdown
		}
	})
	return d.err
}

func (d *deferError) respond(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.errCh == nil || d.responded {
		return
	}
	d.responded = true
	d.mu.Unlock()
	d.errCh <- err
	close(d.errCh)
	d.mu.Lock()
}

// ErrorCh возвращает канал, по которому приходит ошибка выполнения future.
// Позволяет использовать select для ожидания без создания го-рутины.
func (d *deferError) ErrorCh() <-chan error {
	return d.errCh
}

// logFuture — future для одной записи журнала.
type logFuture struct {
	deferError

	log      LogEntry
	response any
	dispatch time.Time
}

func (l *logFuture) Index() int {
	return l.log.Index
}

func (l *logFuture) Response() any {
	return l.response
}

// configurationsFuture — future для GetConfiguration.
type configurationsFuture struct {
	deferError

	config Configuration
}

func (f *configurationsFuture) Index() int {
	return 0
}

func (f *configurationsFuture) Configuration() Configuration {
	return f.config
}

// LeadershipTransferFuture используется для ожидания результата
// передачи лидерства от одного узла другому.
type LeadershipTransferFuture interface {
	Future
}

// leadershipTransferFuture — future для отслеживания передачи лидерства.
type leadershipTransferFuture struct {
	deferError

	targetID ServerID
}

// errorFuture — future с заранее известной ошибкой.
type errorFuture struct {
	err error
}

func (e errorFuture) Error() error { return e.err }
func (e errorFuture) ErrorCh() <-chan error {
	ch := make(chan error, 1)
	ch <- e.err
	close(ch)
	return ch
}
func (e errorFuture) Index() int                   { return 0 }
func (e errorFuture) Response() any                { return nil }
func (e errorFuture) Configuration() Configuration { return Configuration{} }

// reqSnapshotFuture — внутренний future для запроса снимка у runFSM.
type reqSnapshotFuture struct {
	deferError

	index    int
	term     int
	snapshot FSMSnapshot
}

// SnapshotFuture — публичный future для пользовательского Snapshot().
type SnapshotFuture struct {
	deferError
}
