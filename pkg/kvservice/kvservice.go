// Package kvservice KV-сервис на основе Raft — основной файл реализации.
package kvservice

import (
	"context"
	"encoding/gob"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vskurikhin/raft/pkg/api"
	"github.com/vskurikhin/raft/pkg/raft"
)

const DebugKV = 1

type KVService struct {
	mu sync.Mutex

	// id — идентификатор сервиса в кластере Raft.
	id int

	// rs — сервер Raft, содержащий экземпляр ConsensusModule (CM).
	rs *raft.Server

	// commitChan — канал фиксации, передаваемый серверу Raft. После фиксации
	// команд они отправляются через этот канал.
	commitChan chan raft.CommitEntry

	// commitSubs — активные подписки на события фиксации команд в данном
	// сервисе. Подробнее см. метод createCommitSubscription.
	commitSubs map[int]chan Command

	// ds — базовое хранилище данных, реализующее KV-базу данных.
	ds *DataStore

	// srv — HTTP-сервер, через который сервис предоставляет внешний API.
	srv *http.Server

	// lastRequestIDPerClient используется для устранения дублирующихся
	// клиентских запросов. Для каждого клиента хранится идентификатор
	// последнего запроса, обработанного обновляющей горутиной.
	// Предполагается, что идентификаторы клиентов (ключи карты) уникальны,
	// а идентификаторы запросов каждого клиента (значения карты)
	// также уникальны и монотонно возрастают.
	lastRequestIDPerClient map[int64]int64

	// delayNextHTTPResponse указывает, что следующий HTTP-ответ клиенту
	// должен быть искусственно задержан. После использования флаг
	// автоматически сбрасывается.
	delayNextHTTPResponse atomic.Bool
}

// New создаёт новый экземпляр KVService.
//
//   - address - адрес который будет слушать Raft сервер.
//   - id — идентификатор данного сервиса в кластере Raft.
//   - peerIds — идентификаторы остальных узлов Raft в кластере.
//   - storage — реализация интерфейса raft.Storage, используемая сервисом
//     для долговременного хранения и сохранения своего состояния.
//   - readyChan — канал уведомления, который должен быть закрыт после того,
//     как кластер Raft будет готов к работе (все узлы запущены и соединены
//     друг с другом).
func New(address string, id int, peerIds []int, storage raft.Storage, readyChan <-chan any) *KVService {
	gob.Register(Command{})
	commitChan := make(chan raft.CommitEntry)

	// raft.Server обрабатывает RPC-вызовы протокола Raft в кластере.
	// После вызова Serve сервер готов принимать RPC-соединения
	// от остальных узлов.
	rs := raft.NewServer(id, peerIds, storage, readyChan, commitChan)
	rs.Serve(address)
	kvs := &KVService{
		id:                     id,
		rs:                     rs,
		commitChan:             commitChan,
		ds:                     NewDataStore(),
		commitSubs:             make(map[int]chan Command),
		lastRequestIDPerClient: make(map[int64]int64),
	}

	kvs.runUpdater()
	return kvs
}

// IsLeader проверяет, считает ли kvs себя лидером кластера Raft.
// Используется только для тестирования и отладки.
func (kvs *KVService) IsLeader() bool {
	return kvs.rs.IsLeader()
}

// ServeHTTP запускает HTTP-сервер, предоставляющий REST API KV-сервиса
// на указанном TCP-порту. Метод не блокирует выполнение: он запускает
// HTTP-сервер в отдельной горутине и сразу возвращает управление.
// Для корректной остановки сервера вызовите метод Shutdown.
func (kvs *KVService) ServeHTTP(port int) {
	if kvs.srv != nil {
		panic("ServeHTTP called with existing server")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /get/", kvs.handleGet)
	mux.HandleFunc("POST /put/", kvs.handlePut)
	mux.HandleFunc("POST /append/", kvs.handleAppend)
	mux.HandleFunc("POST /cas/", kvs.handleCAS)

	kvs.srv = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		kvs.kvLogf("serving HTTP on %s", kvs.srv.Addr)
		if err := kvs.srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
		kvs.srv = nil
	}()
}

// Shutdown корректно завершает работу сервиса: останавливает RPC-сервер
// Raft и основной HTTP-сервер. Метод возвращает управление только после
// полного завершения процедуры остановки.
//
// Примечание: перед вызовом Shutdown необходимо вызвать
// DisconnectFromRaftPeers для всех узлов кластера.
func (kvs *KVService) Shutdown() error {
	kvs.kvLogf("shutting down Raft server")
	kvs.rs.Shutdown()
	kvs.kvLogf("closing commitChan")
	close(kvs.commitChan)

	if kvs.srv != nil {
		kvs.kvLogf("shutting down HTTP server")
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		_ = kvs.srv.Shutdown(ctx)
		kvs.kvLogf("HTTP shutdown complete")

		return nil
	}

	return nil
}

// DelayNextHTTPResponse указывает сервису задержать отправку ответа на
// следующий HTTP-запрос клиента. Сам запрос обрабатывается как обычно,
// задерживается только отправка HTTP-ответа. Задержка применяется только
// к одному ответу — после использования флаг автоматически сбрасывается.
func (kvs *KVService) DelayNextHTTPResponse() {
	kvs.delayNextHTTPResponse.Store(true)
}

func (kvs *KVService) sendHTTPResponse(w http.ResponseWriter, v any) {
	if kvs.delayNextHTTPResponse.Load() {
		kvs.delayNextHTTPResponse.Store(false)
		time.Sleep(300 * time.Millisecond)
	}
	kvs.kvLogf("sending response %#v", v)
	renderJSON(w, v)
}

func (kvs *KVService) handlePut(w http.ResponseWriter, req *http.Request) {
	pr := &api.PutRequest{}
	if err := readRequestJSON(req, pr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kvs.kvLogf("HTTP PUT %v", pr)

	// Отправить команду серверу Raft. Это изменение состояния реплицируемой
	// машины состояний, построенной поверх журнала Raft.
	cmd := Command{
		Kind:      CommandPut,
		Key:       pr.Key,
		Value:     pr.Value,
		ServiceID: kvs.id,
		ClientID:  pr.ClientID,
		RequestID: pr.RequestID,
	}
	logIndex := kvs.rs.Submit(cmd)
	// Если мы не являемся лидером Raft, вернуть соответствующий статус.
	if logIndex < 0 {
		kvs.sendHTTPResponse(w, api.PutResponse{RespStatus: api.StatusNotLeader})
		return
	}

	// Создать подписку на фиксацию записи с данным индексом журнала,
	// затем дождаться соответствующего уведомления.
	sub := kvs.createCommitSubscription(logIndex)

	// Ожидать получения сообщения по каналу подписки: горутина updater
	// передаст значение, когда запись с индексом logIndex будет зафиксирована
	// в журнале Raft. Для корректного завершения работы сервиса также
	// отслеживается контекст запроса: если запрос отменён, обработчик
	// прекращает работу, не отправляя ответ клиенту.
	select {
	case commitCmd := <-sub:
		// Если это наша команда — всё в порядке. Если же была зафиксирована
		// команда другого сервиса, значит, в какой-то момент мы утратили
		// лидерство и должны вернуть клиенту сообщение об ошибке.
		if commitCmd.ServiceID == kvs.id {
			if commitCmd.IsDuplicate {
				// Если команда является дубликатом, она не была выполнена
				// в результате текущего запроса. Сообщить клиенту
				// специальный статус.
				kvs.sendHTTPResponse(w, api.PutResponse{
					RespStatus: api.StatusDuplicateRequest,
				})
			} else {
				kvs.sendHTTPResponse(w, api.PutResponse{
					RespStatus: api.StatusOK,
					KeyFound:   commitCmd.ResultFound,
					PrevValue:  commitCmd.ResultValue,
				})
			}
		} else {
			kvs.sendHTTPResponse(w, api.PutResponse{RespStatus: api.StatusFailedCommit})
		}
	case <-req.Context().Done():
		return
	}
}

// Реализация этих обработчиков очень похожа на handlePut.
// Подробные комментарии см. в описании этой функции.
func (kvs *KVService) handleAppend(w http.ResponseWriter, req *http.Request) {
	ar := &api.AppendRequest{}
	if err := readRequestJSON(req, ar); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kvs.kvLogf("HTTP APPEND %v", ar)

	cmd := Command{
		Kind:      CommandAppend,
		Key:       ar.Key,
		Value:     ar.Value,
		ServiceID: kvs.id,
		ClientID:  ar.ClientID,
		RequestID: ar.RequestID,
	}
	logIndex := kvs.rs.Submit(cmd)
	if logIndex < 0 {
		kvs.sendHTTPResponse(w, api.AppendResponse{RespStatus: api.StatusNotLeader})
		return
	}

	sub := kvs.createCommitSubscription(logIndex)

	select {
	case commitCmd := <-sub:
		if commitCmd.ServiceID == kvs.id {
			if commitCmd.IsDuplicate {
				kvs.sendHTTPResponse(w, api.AppendResponse{
					RespStatus: api.StatusDuplicateRequest,
				})
			} else {
				kvs.sendHTTPResponse(w, api.AppendResponse{
					RespStatus: api.StatusOK,
					KeyFound:   commitCmd.ResultFound,
					PrevValue:  commitCmd.ResultValue,
				})
			}
		} else {
			kvs.sendHTTPResponse(w, api.AppendResponse{RespStatus: api.StatusFailedCommit})
		}
	case <-req.Context().Done():
		return
	}
}

func (kvs *KVService) handleGet(w http.ResponseWriter, req *http.Request) {
	gr := &api.GetRequest{}
	if err := readRequestJSON(req, gr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kvs.kvLogf("HTTP GET %v", gr)

	cmd := Command{
		Kind:      CommandGet,
		Key:       gr.Key,
		ServiceID: kvs.id,
		ClientID:  gr.ClientID,
		RequestID: gr.RequestID,
	}
	logIndex := kvs.rs.Submit(cmd)
	if logIndex < 0 {
		kvs.sendHTTPResponse(w, api.GetResponse{RespStatus: api.StatusNotLeader})
		return
	}

	sub := kvs.createCommitSubscription(logIndex)

	select {
	case commitCmd := <-sub:
		if commitCmd.ServiceID == kvs.id {
			if commitCmd.IsDuplicate {
				kvs.sendHTTPResponse(w, api.GetResponse{
					RespStatus: api.StatusDuplicateRequest,
				})
			} else {
				kvs.sendHTTPResponse(w, api.GetResponse{
					RespStatus: api.StatusOK,
					KeyFound:   commitCmd.ResultFound,
					Value:      commitCmd.ResultValue,
				})
			}
		} else {
			kvs.sendHTTPResponse(w, api.GetResponse{RespStatus: api.StatusFailedCommit})
		}
	case <-req.Context().Done():
		return
	}
}

func (kvs *KVService) handleCAS(w http.ResponseWriter, req *http.Request) {
	cr := &api.CASRequest{}
	if err := readRequestJSON(req, cr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kvs.kvLogf("HTTP CAS %v", cr)

	cmd := Command{
		Kind:         CommandCAS,
		Key:          cr.Key,
		Value:        cr.Value,
		CompareValue: cr.CompareValue,
		ServiceID:    kvs.id,
		ClientID:     cr.ClientID,
		RequestID:    cr.RequestID,
	}
	logIndex := kvs.rs.Submit(cmd)
	if logIndex < 0 {
		kvs.sendHTTPResponse(w, api.CASResponse{RespStatus: api.StatusNotLeader})
		return
	}

	sub := kvs.createCommitSubscription(logIndex)

	select {
	case commitCmd := <-sub:
		if commitCmd.ServiceID == kvs.id {
			if commitCmd.IsDuplicate {
				kvs.sendHTTPResponse(w, api.CASResponse{
					RespStatus: api.StatusDuplicateRequest,
				})
			} else {
				kvs.sendHTTPResponse(w, api.CASResponse{
					RespStatus: api.StatusOK,
					KeyFound:   commitCmd.ResultFound,
					PrevValue:  commitCmd.ResultValue,
				})
			}
		} else {
			kvs.sendHTTPResponse(w, api.CASResponse{RespStatus: api.StatusFailedCommit})
		}
	case <-req.Context().Done():
		return
	}
}

// runUpdater запускает горутину "updater", которая читает канал фиксации
// (commit channel) Raft и обновляет хранилище данных. Именно эта часть
// реализует реплицируемую машину состояний (Replicated State Machine)
// распределённого консенсуса.
// Кроме того, updater уведомляет подписчиков, зарегистрированных через
// createCommitSubscription.
func (kvs *KVService) runUpdater() {
	go func() {
		for entry := range kvs.commitChan {
			cmd := entry.Command.(Command)

			// Обнаружение дублирующихся команд.
			// Запрос принимается только в том случае, если его идентификатор
			// больше идентификатора последнего обработанного запроса
			// данного клиента.
			lastReqID, ok := kvs.lastRequestIDPerClient[cmd.ClientID]
			if ok && lastReqID >= cmd.RequestID {
				kvs.kvLogf("duplicate request id=%v, from client id=%v", cmd.RequestID, cmd.ClientID)
				// Дубликат: запрос с таким идентификатором уже был обработан ранее.
				cmd = Command{
					Kind:        cmd.Kind,
					IsDuplicate: true,
				}
			} else {
				kvs.lastRequestIDPerClient[cmd.ClientID] = cmd.RequestID

				switch cmd.Kind {
				case CommandGet:
					cmd.ResultValue, cmd.ResultFound = kvs.ds.Get(cmd.Key)
				case CommandPut:
					cmd.ResultValue, cmd.ResultFound = kvs.ds.Put(cmd.Key, cmd.Value)
				case CommandAppend:
					cmd.ResultValue, cmd.ResultFound = kvs.ds.Append(cmd.Key, cmd.Value)
				case CommandCAS:
					cmd.ResultValue, cmd.ResultFound = kvs.ds.CAS(cmd.Key, cmd.CompareValue, cmd.Value)
				default:
					panic(fmt.Errorf("unexpected command %v", cmd))
				}
			}

			// Передать команду подписчику, ожидающему фиксации записи
			// с данным индексом журнала, после чего закрыть подписку,
			// поскольку она является одноразовой.
			if sub := kvs.popCommitSubscription(entry.Index); sub != nil {
				sub <- cmd
				close(sub)
			}
		}
	}()
}

// createCommitSubscription создаёт "подписку на фиксацию" для указанного
// индекса журнала. Она используется обработчиками клиентских запросов,
// отправляющими команды в ConsensusModule Raft.
// Вызов createCommitSubscription(index) означает:
// "уведомить меня, когда запись с этим индексом будет зафиксирована
// в журнале Raft".
// После фиксации записи горутина updater отправляет её через возвращаемый
// (буферизированный) канал, затем закрывает канал, автоматически отменяя
// подписку.
func (kvs *KVService) createCommitSubscription(logIndex int) chan Command {
	kvs.mu.Lock()
	defer kvs.mu.Unlock()

	if _, exists := kvs.commitSubs[logIndex]; exists {
		panic(fmt.Sprintf("duplicate commit subscription for logIndex=%d", logIndex))
	}

	ch := make(chan Command, 1)
	kvs.commitSubs[logIndex] = ch
	return ch
}

func (kvs *KVService) popCommitSubscription(logIndex int) chan Command {
	kvs.mu.Lock()
	defer kvs.mu.Unlock()

	ch := kvs.commitSubs[logIndex]
	delete(kvs.commitSubs, logIndex)
	return ch
}

// kvLogf выводит отладочное сообщение, если DebugKV > 0.
func (kvs *KVService) kvLogf(format string, args ...any) {
	if DebugKV > 0 {
		format = fmt.Sprintf("[kv %d] ", kvs.id) + format
		log.Printf(format, args...)
	}
}

// Следующие функции существуют исключительно для целей тестирования
// и используются для моделирования различных сбоев.

func (kvs *KVService) ConnectToRaftPeer(peerID int, addr net.Addr) error {
	return kvs.rs.ConnectToPeer(peerID, addr)
}

func (kvs *KVService) DisconnectFromAllRaftPeers() {
	kvs.rs.DisconnectAll()
}

func (kvs *KVService) DisconnectFromRaftPeer(peerID int) error {
	return kvs.rs.DisconnectPeer(peerID)
}

func (kvs *KVService) GetRaftListenAddr() net.Addr {
	return kvs.rs.GetListenAddr()
}
