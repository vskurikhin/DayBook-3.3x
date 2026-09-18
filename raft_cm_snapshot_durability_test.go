package raft

import (
	"bytes"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/fortytw2/leaktest"
)

// errStorageWriteAborted — значение паники прерывающего хранилища.
var errStorageWriteAborted = errors.New("storage write aborted")

// abortOnWriteStorage — хранилище, прерывающее исполнение на первой записи
// после взведения. Моделирует единственный способ, которым постоянное
// хранилище может сообщить о невозможности записи: Storage.Set не
// возвращает ошибку, поэтому FileStorage при отказе записи завершает
// исполнение через log.Fatalf. Прерывание внутри процесса позволяет
// наблюдать, что узел успел пообещать лидеру к моменту отказа записи.
type abortOnWriteStorage struct {
	*MapStorage

	armed atomic.Bool
	hits  atomic.Int64
}

func (s *abortOnWriteStorage) Set(key string, value []byte) {
	if s.armed.Load() {
		s.hits.Add(1)
		panic(errStorageWriteAborted)
	}
	s.MapStorage.Set(key, value)
}

// newAbortOnWriteInstallSnapshotCM собирает ведомого с уже установленным
// снимком (lastSnapshotIndex=10, терм 2, пустой журнал) и хранилищем,
// прерывающим запись постоянного состояния.
func newAbortOnWriteInstallSnapshotCM() (*ConsensusModule, *abortOnWriteStorage) {
	storage := &abortOnWriteStorage{MapStorage: NewMapStorage()}
	cm, _ := newInstallSnapshotCM()
	cm.storage = storage
	return cm, storage
}

// runInstallSnapshotUntilAbort доставляет InstallSnapshot в обработчик из
// отдельной горутины и возвращает ответ, отправленный отложенным блоком.
// Прерывание записи постоянного состояния поглощается; любая другая паника
// пробрасывается дальше.
func runInstallSnapshotUntilAbort(
	t *testing.T, cm *ConsensusModule, req *InstallSnapshotRequest, data []byte,
) InstallSnapshotResponse {
	t.Helper()
	respCh := make(chan RPCResponse, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if err, ok := r.(error); ok && errors.Is(err, errStorageWriteAborted) {
				return
			}
			panic(r)
		}()
		cm.handleInstallSnapshot(RPC{Command: req, Reader: bytes.NewReader(data), RespChan: respCh}, req)
	}()
	<-done

	resp := <-respCh
	reply, ok := resp.Reply.(*InstallSnapshotResponse)
	if !ok {
		t.Fatalf("unexpected reply type %T", resp.Reply)
	}
	return *reply
}

// TestInstallSnapshot_NotSuccessfulBeforePersist проверяет порядок
// «персист → Success»: если запись постоянного состояния не завершилась,
// ведомый не имеет права сообщить лидеру об успешной установке снимка.
// Ответ об успехе, выставленный до персиста, означал бы, что лидер
// перестанет досылать снимок, а ведомый после перезапуска его не найдёт.
func TestInstallSnapshot_NotSuccessfulBeforePersist(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm, storage := newAbortOnWriteInstallSnapshotCM()
	data := installSnapshotRequestData(t, map[string]string{"k0": "v0"})
	req := installSnapshotRequest(99, 12, 2, data) // новее имеющегося (10)

	storage.armed.Store(true)
	reply := runInstallSnapshotUntilAbort(t, cm, req, data)

	if got := storage.hits.Load(); got == 0 {
		t.Fatal("запись постоянного состояния не была достигнута: приспособление не проверяет порядок")
	}
	if reply.Success {
		t.Fatal("Success=true при незавершённой записи постоянного состояния: ответ выставлен до персиста")
	}
}

// TestInstallSnapshot_StateDurableOnSuccess проверяет вторую половину того
// же порядка: к моменту, когда ведомый отвечает Success, водяные знаки
// снимка и уплотнённый журнал уже лежат в хранилище.
func TestInstallSnapshot_StateDurableOnSuccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm, _ := newInstallSnapshotCM()
	storage, ok := cm.storage.(*MapStorage)
	if !ok {
		t.Fatalf("unexpected storage type %T", cm.storage)
	}
	data := installSnapshotRequestData(t, map[string]string{"k0": "v0"})
	req := installSnapshotRequest(99, 12, 2, data) // новее имеющегося (10)

	reply := driveInstallSnapshotRPC(t, cm, req, data)
	if !reply.Success {
		t.Fatalf("InstallSnapshot: Success=false: %+v", reply)
	}

	logData, found := storage.Get("log")
	if !found {
		t.Fatal("ключ log отсутствует в хранилище при Success=true")
	}
	var onDisk []LogEntry
	gobDecode(t, logData, &onDisk)
	if len(onDisk) != 0 {
		t.Fatalf("журнал в хранилище = %+v, want пустой (уплотнён по индексу снимка)", onDisk)
	}

	for key, want := range map[string]int{"lastSnapshotIndex": 12, "lastSnapshotTerm": 2} {
		raw, found := storage.Get(key)
		if !found {
			t.Fatalf("ключ %s отсутствует в хранилище при Success=true", key)
		}
		var got int
		gobDecode(t, raw, &got)
		if got != want {
			t.Fatalf("%s в хранилище = %d, want %d", key, got, want)
		}
	}
}
