// Package kvservice KV-сервис на основе Raft — основной файл реализации.
package kvservice

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/api"
	"github.com/vskurikhin/raft/pkg/raft/transp"
)

// _traceKV — порог детализации отладочных сообщений KV-сервиса (traceLogf).
// Сообщение выводится, если _traceKV > 0. Значение задаётся полем
// TraceConfig.Level единственного успешного вызова SetTrace, по
// умолчанию 0. Изменяется только до старта горутин.
var _traceKV = 0

// _traceLogger — логгер отладочных сообщений KV-сервиса (traceLogf).
// По умолчанию выводит в стандартный логгер (stderr). Перенаправляется
// в файл функцией SetTrace.
var _traceLogger = log.Default()

// _traceConfigured — сторожевой флаг строгого set-once: единственная
// успешная конфигурация трассировки на процесс уже выполнена.
// Устанавливается только успешным вызовом SetTrace
// (вызов, завершившийся ошибкой I/O, окно не расходует).
var _traceConfigured atomic.Bool

// _traceCMCreated — сторожевой флаг: в процессе уже создавался
// KVService. Устанавливается конструктором New/NewKVService.
var _traceCMCreated atomic.Bool

// TraceConfig — параметры трассировки KV-сервиса, передаваемые SetTrace.
// Тип является простым носителем значений: у него нет методов и он не
// подразумевает расширения поведением.
type TraceConfig struct {
	// Level — порог детализации: трассировка KV-сервиса печатается,
	// если Level > 0. Level == 0 означает, что трассировка выключена;
	// нулевое значение НЕ трактуется как «уровень по умолчанию 1».
	Level int

	// LogFile — путь к файлу журнала трассировки. Файл открывается в
	// режиме добавления и создаётся при необходимости. Пустая строка
	// означает вывод в стандартный логгер (stderr).
	LogFile string
}

// SetTrace конфигурирует трассировку KV-сервиса (traceLogf): порог
// детализации cfg.Level и назначение вывода cfg.LogFile.
// Пустой cfg.LogFile — вывод в стандартный логгер;
// иначе файл открывается в режиме добавления и создаётся при необходимости.
// Значение cfg.Level == 0 выключает трассировку и не подменяется значением
// по умолчанию.
//
// SetTrace — единственная точка конфигурации трассировки пакета.
func SetTrace(cfg TraceConfig) error {
	if _traceConfigured.Load() || _traceCMCreated.Load() {
		return errors.New(
			"kvservice: trace configuration must be set exactly once, " +
				"before the first KVService is created; repeated calls are forbidden",
		)
	}
	if cfg.LogFile == "" {
		_traceKV = cfg.Level
		_traceLogger = log.Default()
		_traceConfigured.Store(true)
		return nil
	}
	// Порог присваивается только после успешного открытия файла.
	f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_traceKV = cfg.Level
	_traceLogger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	_traceConfigured.Store(true)
	return nil
}

// _requestTimeout — таймаут для Apply-операций (PUT, CAS, DELETE, GET).
// Если за это время не удалось отправить команду в applyCh лидера,
// возвращается contract.ErrEnqueueTimeout.
const _requestTimeout = 10 * time.Second

// _httpShutdownTimeout — тайм-аут плавной остановки HTTP-сервера
// сервиса: ожидание завершения обработчиков в Shutdown; по истечении
// слушатель закрывается принудительно.
const _httpShutdownTimeout = 200 * time.Millisecond

type KVService struct {
	// id — идентификатор сервиса в кластере Raft.
	id int

	// rs — сервер Raft, содержащий экземпляр ConsensusModule (CM).
	rs *raft.Server

	// ds — базовое хранилище данных, реализующее KV-базу данных.
	ds *DataStore

	// srv — HTTP-сервер, через который сервис предоставляет внешний API.
	// Устанавливается один раз в ServeHTTP до старта Serve-горутины и
	// после этого не изменяется (владение сервером и слушателем
	// закреплено за структурой KVService; прежнее присваивание nil из
	// Serve-горутины было data race).
	srv *http.Server

	// ln — слушатель HTTP-сервера. Устанавливается один раз в ServeHTTP
	// до старта Serve-горутины и не изменяется. Позволяет узнать
	// фактический адрес при address == ":0" (GetHTTPListenAddr).
	ln net.Listener

	// serveErrCh — канал доставки ошибки http.Server.Serve (кроме
	// штатного ErrServerClosed). Буфер 1: Serve завершается один раз.
	// Заменяет прежний log.Fatal из Serve-горутины, недопустимый
	// в тестах.
	serveErrCh chan error

	// shutdownOnce обеспечивает идемпотентность Shutdown.
	shutdownOnce sync.Once

	// httpResponsesEnabled controls whether this service returns HTTP responses
	// to the client. It's only used for testing and debugging.
	// atomic.Bool: пишется из тестовой горутины
	// (ToggleHTTPResponsesEnabled), читается из HTTP-обработчиков.
	httpResponsesEnabled atomic.Bool
}

var _ raft.BatchingFSM = (*KVService)(nil)

// Config — конфигурация для создания нового KVService.
// Встраивает raft.Config и добавляет HTTPAddress — адрес,
// на котором сервис будет принимать HTTP-запросы клиентов.
type Config struct {
	raft.Config

	HTTPAddress string
}

// New создаёт новый экземпляр KVService с заданной конфигурацией cfg,
// хранилищем storage и каналом уведомления readyChan.
// cfg содержит параметры Raft-сервера (идентификатор, список узлов,
// RPC-адрес) и HTTP-адрес для REST API сервиса.
//
// KVService реализует raft.FSM: зафиксированные записи журнала применяются
// непосредственно к DataStore через метод Apply.
func New(cfg *Config, readyChan <-chan any) *KVService {
	gob.Register(Command{})

	// Сторожевой флаг контракта трассировки: конфигурация SetTrace
	// разрешена только до создания первого сервиса (строгий set-once).
	_traceCMCreated.Store(true)

	kvs := &KVService{
		id:         cfg.ServerID,
		ds:         NewDataStore(),
		serveErrCh: make(chan error, 1),
	}
	kvs.httpResponsesEnabled.Store(true)
	cfg.Fsm = kvs

	// raft.Server обрабатывает RPC-вызовы протокола Raft в кластере.
	// KVService передаётся как FSM, поэтому Apply будет вызываться Raft'ом
	// для каждой зафиксированной записи.
	rs := raft.New(&cfg.Config, readyChan)
	rs.Serve()
	kvs.rs = rs

	return kvs
}

// NewKVService создаёт новый экземпляр KVService.
//
//   - address - адрес, который будет слушать Raft сервер; используется
//     для создания TCP-транспорта.
//   - id — идентификатор данного сервиса в кластере Raft.
//   - peerIds — идентификаторы остальных узлов Raft в кластере.
//   - storage — реализация интерфейса raft.Storage, используемая сервисом
//     для долговременного хранения и сохранения своего состояния.
//   - readyChan — канал уведомления, который должен быть закрыт после того,
//     как кластер Raft будет готов к работе (все узлы запущены и соединены
//     друг с другом).
func NewKVService(address string, id int, peerIds []int, storage raft.Storage, readyChan <-chan any) *KVService {
	transport, err := transp.NewTCPTransport(address, 0, 0)
	if err != nil {
		log.Fatalf("kvservice: failed to create TCP transport on %s: %v", address, err)
	}
	return New(&Config{
		Config: raft.Config{
			ServerID:  id,
			PeerIds:   peerIds,
			Storage:   storage,
			Transport: transport,
		},
	}, readyChan,
	)
}

// Apply реализует raft.FSM. Вызывается Raft'ом для каждой зафиксированной
// записи журнала. Команда применяется к DataStore, результат сохраняется
// в полях ResultValue/ResultFound команды и возвращается в future клиента.
func (kvs *KVService) Apply(log *raft.LogEntry) any {
	cmd, ok := log.Data.(Command)
	if !ok {
		kvs.traceLogf("unknown command type %T", log.Data)
		return nil
	}

	switch cmd.Kind {
	case CommandGet:
		cmd.ResultValue, cmd.ResultFound = kvs.ds.Get(cmd.Key)
	case CommandPut:
		cmd.ResultValue, cmd.ResultFound = kvs.ds.Put(cmd.Key, cmd.Value)
	case CommandCAS:
		cmd.ResultValue, cmd.ResultFound = kvs.ds.CAS(cmd.Key, cmd.CompareValue, cmd.Value)
	case CommandDelete:
		cmd.ResultValue, cmd.ResultFound = kvs.ds.Delete(cmd.Key)
	default:
		kvs.traceLogf("unknown command kind %v", cmd.Kind)
		return nil
	}

	return cmd
}

// Snapshot реализует raft.FSM.Snapshot.
// Возвращает снимок текущего состояния DataStore.
func (kvs *KVService) Snapshot() (raft.FSMSnapshot, error) {
	data, err := kvs.ds.Snapshot()
	if err != nil {
		return nil, err
	}
	return &kvSnapshot{data: data}, nil
}

// Restore реализует raft.FSM.Restore.
// Восстанавливает состояние DataStore из снимка.
func (kvs *KVService) Restore(reader io.ReadCloser) error {
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	return kvs.ds.Restore(data)
}

// kvSnapshot — снимок состояния KVService для raft.FSMSnapshot.
type kvSnapshot struct {
	data []byte
}

func (s *kvSnapshot) Persist(sink raft.SnapshotSink) error {
	_, err := sink.Write(s.data)
	return err
}

func (s *kvSnapshot) Release() {}

// ApplyBatch реализует интерфейс raft.BatchingFSM. Вызывается Raft'ом
// для группового применения зафиксированных записей журнала. Каждая запись
// применяется к DataStore через Apply, результат (Command с заполненными
// полями ResultValue/ResultFound) возвращается в срезе ответов для
// сопоставления с соответствующими future клиентов.
func (kvs *KVService) ApplyBatch(logs []*raft.LogEntry) []any {
	results := make([]any, 0, len(logs))
	for _, l := range logs {
		cmd := kvs.Apply(l)
		results = append(results, cmd)
	}
	return results
}

// IsLeader проверяет, считает ли kvs себя лидером кластера Raft.
// Используется только для тестирования и отладки.
func (kvs *KVService) IsLeader() bool {
	return kvs.rs.IsLeader()
}

// ServeHTTP запускает HTTP-сервер, предоставляющий REST API KV-сервиса
// на указанном TCP-адресе. Метод не блокирует выполнение: слушатель
// открывается синхронно (поэтому фактический порт известен сразу после
// возврата — см. GetHTTPListenAddr, в том числе при address == ":0"),
// а обслуживание запускается в отдельной горутине.
//
// Ошибка открытия слушателя возвращается вызывающему; ошибка Serve
// (кроме штатного http.ErrServerClosed) доставляется через ServeErr()
// log.Fatal из горутины недопустим.
//
// Для корректной остановки сервера вызовите метод Shutdown.
func (kvs *KVService) ServeHTTP(address string) error {
	if kvs.srv != nil {
		return fmt.Errorf("kvservice %d: ServeHTTP called with existing server", kvs.id)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /get/", kvs.handleGet)
	mux.HandleFunc("POST /weak-get/", kvs.handleWeakGet)
	mux.HandleFunc("POST /put/", kvs.handlePut)
	mux.HandleFunc("POST /cas/", kvs.handleCAS)
	mux.HandleFunc("POST /verifyleader/", kvs.handleVerifyLeader)
	mux.HandleFunc("POST /delete/", kvs.handleDelete)

	ln, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("kvservice %d: listen %q: %w", kvs.id, address, err)
	}

	// srv и ln устанавливаются ДО старта Serve-горутины и далее
	// не изменяются — читатели (GetHTTPListenAddr, Shutdown) видят
	// неизменяемые значения.
	kvs.ln = ln
	kvs.srv = &http.Server{
		Addr:    address,
		Handler: mux,
	}

	kvs.traceLogf("serving HTTP on %s", ln.Addr().String())
	go func() {
		if err := kvs.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			kvs.serveErrCh <- err
		}
		close(kvs.serveErrCh)
	}()
	return nil
}

// GetHTTPListenAddr возвращает фактический адрес слушателя HTTP-сервера
// (актуально при address == ":0"). Пустая строка — ServeHTTP не вызывался.
func (kvs *KVService) GetHTTPListenAddr() string {
	if kvs.ln == nil {
		return ""
	}
	return kvs.ln.Addr().String()
}

// ServeErr возвращает канал ошибок HTTP-сервера. Канал закрывается
// после завершения Serve; штатное завершение (http.ErrServerClosed)
// ошибкой не считается и в канал не отправляется.
func (kvs *KVService) ServeErr() <-chan error {
	return kvs.serveErrCh
}

// Shutdown корректно завершает работу сервиса: останавливает RPC-сервер
// Raft и основной HTTP-сервер. Метод возвращает управление только после
// полного завершения процедуры остановки. Идемпотентен — повторные
// вызовы безопасны и возвращают nil.
//
// Примечание: перед вызовом Shutdown необходимо вызвать
// DisconnectFromRaftPeers для всех узлов кластера.
func (kvs *KVService) Shutdown() error {
	kvs.shutdownOnce.Do(func() {
		kvs.traceLogf("shutting down Raft server")
		kvs.rs.Shutdown()

		if kvs.srv != nil {
			kvs.traceLogf("shutting down HTTP server")
			ctx, cancel := context.WithTimeout(context.Background(), _httpShutdownTimeout)
			defer cancel()
			// srv.Shutdown закрывает слушатель; ln.Close() дополнительно
			// гарантирует освобождение порта при истечении ctx.
			_ = kvs.srv.Shutdown(ctx)
			_ = kvs.ln.Close()
			kvs.traceLogf("HTTP shutdown complete")
		}
	})

	return nil
}

// ToggleHTTPResponsesEnabled управляет тем, будет ли этот сервис отправлять
// HTTP-ответы клиентам. В обычном режиме работы эта возможность всегда
// включена. Для целей тестирования и отладки этот метод можно вызвать
// со значением false; в этом случае сервис не будет отвечать клиентам
// по HTTP.
func (kvs *KVService) ToggleHTTPResponsesEnabled(enable bool) {
	kvs.httpResponsesEnabled.Store(enable)
}

// Следующие функции существуют исключительно для целей тестирования
// и используются для моделирования различных сбоев.

func (kvs *KVService) ConnectToRaftPeer(peerID int, addr net.Addr) error {
	return kvs.rs.ConnectToPeerWithTimeout(peerID, addr, 2*raft.Quantum*time.Second)
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

func (kvs *KVService) sendHTTPResponse(w http.ResponseWriter, v any) {
	if kvs.httpResponsesEnabled.Load() {
		renderJSON(w, v)
	}
}

func (kvs *KVService) handleVerifyLeader(w http.ResponseWriter, _ *http.Request) {
	// ReadIndex-проверка лидерства (Raft §8) без записи в журнал.
	if err := kvs.rs.VerifyLeader().Error(); err != nil {
		kvs.sendHTTPResponse(w, api.StatusResponse{RespStatus: api.StatusNotLeader})
		return
	}
	kvs.sendHTTPResponse(w, api.StatusResponse{RespStatus: api.StatusOK})
}

func (kvs *KVService) handlePut(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	pr := &api.PutRequest{}
	defer func() {
		elapsed := time.Since(start)
		kvs.traceLogf("HTTP PUT %v took %v", pr, elapsed)
	}()

	if err := readRequestJSON(req, pr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cmd := Command{
		Kind:  CommandPut,
		Key:   pr.Key,
		Value: pr.Value,
		ID:    kvs.id,
	}

	future := kvs.rs.Apply(cmd, _requestTimeout)

	select {
	case err := <-future.ErrorCh():
		if err != nil {
			kvs.sendHTTPResponse(w, api.PutResponse{
				RespStatus: api.StatusNotLeader,
			})
			return
		}
		cmdResp, ok := future.Response().(Command)
		if !ok {
			kvs.sendHTTPResponse(w, api.PutResponse{
				RespStatus: api.StatusInvalid,
			})
			return
		}
		kvs.sendHTTPResponse(w, api.PutResponse{
			RespStatus: api.StatusOK,
			KeyFound:   cmdResp.ResultFound,
			PrevValue:  cmdResp.ResultValue,
		})

	case <-req.Context().Done():
		return
	}
}

func (kvs *KVService) handleDelete(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	dr := &api.DeleteRequest{}
	defer func() {
		elapsed := time.Since(start)
		kvs.traceLogf("HTTP DELETE %v took %v", dr, elapsed)
	}()

	if err := readRequestJSON(req, dr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cmd := Command{
		Kind: CommandDelete,
		Key:  dr.Key,
		ID:   kvs.id,
	}

	future := kvs.rs.Apply(cmd, _requestTimeout)

	select {
	case err := <-future.ErrorCh():
		if err != nil {
			kvs.sendHTTPResponse(w, api.DeleteResponse{
				RespStatus: api.StatusNotLeader,
			})
			return
		}
		cmdResp, ok := future.Response().(Command)
		if !ok {
			kvs.sendHTTPResponse(w, api.DeleteResponse{
				RespStatus: api.StatusInvalid,
			})
			return
		}
		kvs.sendHTTPResponse(w, api.DeleteResponse{
			RespStatus: api.StatusOK,
			KeyFound:   cmdResp.ResultFound,
			PrevValue:  cmdResp.ResultValue,
		})

	case <-req.Context().Done():
		return
	}
}

func (kvs *KVService) handleGet(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	gr := &api.GetRequest{}
	defer func() {
		elapsed := time.Since(start)
		kvs.traceLogf("HTTP GET %v took %v", gr, elapsed)
	}()

	if err := readRequestJSON(req, gr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cmd := Command{
		Kind: CommandGet,
		Key:  gr.Key,
		ID:   kvs.id,
	}

	future := kvs.rs.Apply(cmd, _requestTimeout)

	select {
	case err := <-future.ErrorCh():
		if err != nil {
			kvs.sendHTTPResponse(w, api.GetResponse{
				RespStatus: api.StatusNotLeader,
			})
			return
		}
		cmdResp, ok := future.Response().(Command)
		if !ok {
			kvs.sendHTTPResponse(w, api.GetResponse{
				RespStatus: api.StatusInvalid,
			})
			return
		}
		kvs.sendHTTPResponse(w, api.GetResponse{
			RespStatus: api.StatusOK,
			KeyFound:   cmdResp.ResultFound,
			Value:      cmdResp.ResultValue,
		})

	case <-req.Context().Done():
		return
	}
}

func (kvs *KVService) handleWeakGet(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	gr := &api.GetRequest{}
	defer func() {
		elapsed := time.Since(start)
		kvs.traceLogf("HTTP WEAK-GET %v took %v", gr, elapsed)
	}()

	if err := readRequestJSON(req, gr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// ReadIndex: подтверждение лидерства без записи в raft-журнал (Raft §8).
	future := kvs.rs.VerifyLeader()
	select {
	case err := <-future.ErrorCh():
		if err != nil {
			kvs.sendHTTPResponse(w, api.GetResponse{
				RespStatus: api.StatusNotLeader,
			})
			return
		}
	case <-req.Context().Done():
		return
	}

	// Локальное чтение из DataStore (без raft-журнала, только после ReadIndex).
	value, found := kvs.ds.Get(gr.Key)
	kvs.sendHTTPResponse(w, api.GetResponse{
		RespStatus: api.StatusOK,
		KeyFound:   found,
		Value:      value,
	})
}

func (kvs *KVService) handleCAS(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	cr := &api.CASRequest{}
	defer func() {
		elapsed := time.Since(start)
		kvs.traceLogf("HTTP CAS %v took %v", cr, elapsed)
	}()

	if err := readRequestJSON(req, cr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cmd := Command{
		Kind:         CommandCAS,
		Key:          cr.Key,
		CompareValue: cr.CompareValue,
		Value:        cr.Value,
		ID:           kvs.id,
	}

	future := kvs.rs.Apply(cmd, _requestTimeout)

	select {
	case err := <-future.ErrorCh():
		if err != nil {
			kvs.sendHTTPResponse(w, api.CASResponse{
				RespStatus: api.StatusNotLeader,
			})
			return
		}
		cmdResp, ok := future.Response().(Command)
		if !ok {
			kvs.sendHTTPResponse(w, api.CASResponse{
				RespStatus: api.StatusInvalid,
			})
			return
		}
		kvs.sendHTTPResponse(w, api.CASResponse{
			RespStatus: api.StatusOK,
			KeyFound:   cmdResp.ResultFound,
			PrevValue:  cmdResp.ResultValue,
		})

	case <-req.Context().Done():
		return
	}
}

// traceLogf выводит отладочное сообщение, если _traceKV > 0.
func (kvs *KVService) traceLogf(format string, args ...any) {
	if _traceKV > 0 {
		format = fmt.Sprintf("[kv %d] ", kvs.id) + format
		_traceLogger.Printf(format, args...)
	}
}
