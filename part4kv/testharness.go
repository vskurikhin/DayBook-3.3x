package pkg

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vskurikhin/raft/part3/raft"
	"github.com/vskurikhin/raft/part4kv/kvclient"
	"github.com/vskurikhin/raft/part4kv/kvservice"
)

func init() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
}

// Harness Тестовый стенд для системного тестирования kvservice и клиента.
type Harness struct {
	n int

	// kvCluster — список всех экземпляров KVService, участвующих в кластере.
	// Индекс сервиса в этом списке является его идентификатором (ID) в кластере.
	kvCluster []*kvservice.KVService

	// kvServiceAddrs — список HTTP-адресов (localhost:<PORT>), на которых
	// сервисы KV принимают клиентские команды.
	kvServiceAddrs []string

	storage []*raft.MapStorage

	t *testing.T

	// connected содержит по одному логическому значению для каждого сервера
	// кластера и указывает, подключён ли сервер в данный момент к остальным
	// узлам (если false, сервер изолирован, и сообщения не передаются ни к нему,
	// ни от него).
	connected []bool

	// alive содержит по одному логическому значению для каждого сервера
	// кластера и указывает, работает ли он в данный момент (false означает,
	// что сервер был аварийно остановлен и ещё не перезапущен).
	// Если connected == true, то сервер обязательно считается работающим.
	alive []bool

	// ctx — контекст, используемый HTTP-клиентом при выполнении команд в тестах.
	// ctxCancel — функция для его отмены.
	ctx       context.Context
	ctxCancel func()
}

func NewHarness(t *testing.T, n int) *Harness {
	kvss := make([]*kvservice.KVService, n)
	ready := make(chan any)
	connected := make([]bool, n)
	alive := make([]bool, n)
	storage := make([]*raft.MapStorage, n)

	// Создать все экземпляры KVService, входящие в этот кластер.
	for i := range n {
		peerIds := make([]int, 0)
		for p := range n {
			if p != i {
				peerIds = append(peerIds, p)
			}
		}

		storage[i] = raft.NewMapStorage()
		kvss[i] = kvservice.New(":0", i, peerIds, storage[i], ready)
		alive[i] = true
	}

	// Соединить Raft-узлы всех сервисов между собой и закрыть канал ready,
	// уведомив их о том, что кластер полностью готов к работе.
	for i := range n {
		for j := range n {
			if i != j {
				kvss[i].ConnectToRaftPeer(j, kvss[j].GetRaftListenAddr())
			}
		}
		connected[i] = true
	}
	close(ready)

	// Каждый экземпляр KVService обслуживает REST API на отдельном TCP-порту.
	kvServiceAddrs := make([]string, n)
	for i := range n {
		port := 14200 + i
		kvss[i].ServeHTTP(port)

		kvServiceAddrs[i] = fmt.Sprintf("localhost:%d", port)
	}

	ctx, ctxCancel := context.WithCancel(context.Background())

	h := &Harness{
		n:              n,
		kvCluster:      kvss,
		kvServiceAddrs: kvServiceAddrs,
		t:              t,
		connected:      connected,
		alive:          alive,
		storage:        storage,
		ctx:            ctx,
		ctxCancel:      ctxCancel,
	}
	return h
}

func (h *Harness) DisconnectServiceFromPeers(id int) {
	tlog("Disconnect %d", id)
	h.kvCluster[id].DisconnectFromAllRaftPeers()
	for j := 0; j < h.n; j++ {
		if j != id {
			h.kvCluster[j].DisconnectFromRaftPeer(id)
		}
	}
	h.connected[id] = false
}

func (h *Harness) ReconnectServiceToPeers(id int) {
	tlog("Reconnect %d", id)
	for j := 0; j < h.n; j++ {
		if j != id && h.alive[j] {
			if err := h.kvCluster[id].ConnectToRaftPeer(j, h.kvCluster[j].GetRaftListenAddr()); err != nil {
				h.t.Fatal(err)
			}
			if err := h.kvCluster[j].ConnectToRaftPeer(id, h.kvCluster[id].GetRaftListenAddr()); err != nil {
				h.t.Fatal(err)
			}
		}
	}
	h.connected[id] = true
}

// CrashService «аварийно завершает» работу сервиса, отключая его от всех
// соседних узлов, а затем корректно останавливая. Этот экземпляр сервиса
// больше не будет использоваться.
func (h *Harness) CrashService(id int) {
	tlog("Crash %d", id)
	h.DisconnectServiceFromPeers(id)
	h.alive[id] = false
	if err := h.kvCluster[id].Shutdown(); err != nil {
		h.t.Errorf("error while shutting down service %d: %v", id, err)
	}
}

// RestartService «перезапускает» сервис, создавая новый экземпляр и
// подключая его к соседним узлам.
func (h *Harness) RestartService(id int) {
	if h.alive[id] {
		log.Fatalf("id=%d is alive in RestartService", id)
	}
	tlog("Restart %d", id)

	peerIds := make([]int, 0)
	for p := range h.n {
		if p != id {
			peerIds = append(peerIds, p)
		}
	}
	ready := make(chan any)
	h.kvCluster[id] = kvservice.New(":0", id, peerIds, h.storage[id], ready)
	h.kvCluster[id].ServeHTTP(14200 + id)

	h.ReconnectServiceToPeers(id)
	close(ready)
	h.alive[id] = true
	time.Sleep(20 * time.Millisecond)
}

// DisableHTTPResponsesFromService заставляет указанный сервис перестать
// отвечать на HTTP-запросы клиентов (при этом он продолжит выполнять
// запрошенные операции).
func (h *Harness) DisableHTTPResponsesFromService(id int) {
	tlog("Disabling HTTP responses from %d", id)
	h.kvCluster[id].ToggleHTTPResponsesEnabled(false)
}

func (h *Harness) Shutdown() {
	for i := range h.n {
		h.kvCluster[i].DisconnectFromAllRaftPeers()
		h.connected[i] = false
	}

	// Эти вызовы помогают HTTP-серверу внутри KVService корректно завершить работу.
	http.DefaultClient.CloseIdleConnections()
	h.ctxCancel()

	for i := range h.n {
		if h.alive[i] {
			h.alive[i] = false
			if err := h.kvCluster[i].Shutdown(); err != nil {
				h.t.Errorf("error while shutting down service %d: %v", i, err)
			}
		}
	}
}

// NewClient создает нового клиента, который будет обращаться ко всем
// существующим работающим сервисам.
func (h *Harness) NewClient() *kvclient.KVClient {
	var addrs []string
	for i := range h.n {
		if h.alive[i] {
			addrs = append(addrs, h.kvServiceAddrs[i])
		}
	}
	return kvclient.New(addrs)
}

// NewClientWithRandomAddrsOrder создает нового клиента, который будет
// обращаться ко всем существующим работающим сервисам, но в случайном
// порядке адресов.
func (h *Harness) NewClientWithRandomAddrsOrder() *kvclient.KVClient {
	var addrs []string
	for i := range h.n {
		if h.alive[i] {
			addrs = append(addrs, h.kvServiceAddrs[i])
		}
	}
	rand.Shuffle(len(addrs), func(i, j int) {
		addrs[i], addrs[j] = addrs[j], addrs[i]
	})
	return kvclient.New(addrs)
}

// NewClientSingleService создает нового клиента, который будет обращаться
// только к одному сервису (указанному по id). Обратите внимание, что если
// этот сервис не является лидером, клиент может бесконечно выполнять
// повторные попытки.
func (h *Harness) NewClientSingleService(id int) *kvclient.KVClient {
	addrs := h.kvServiceAddrs[id : id+1]
	return kvclient.New(addrs)
}

// CheckSingleLeader проверяет, что только один сервер считает себя лидером.
// Возвращает идентификатор лидера в кластере Raft. Если лидер еще не выбран,
// метод несколько раз повторяет проверку, поэтому его также удобно
// использовать для ожидания завершения выборов и готовности кластера
// выполнять команды.
func (h *Harness) CheckSingleLeader() int {
	for r := 0; r < 8; r++ {
		leaderId := -1
		for i := range h.n {
			if h.connected[i] && h.kvCluster[i].IsLeader() {
				if leaderId < 0 {
					leaderId = i
				} else {
					h.t.Fatalf("both %d and %d think they're leaders", leaderId, i)
				}
			}
		}
		if leaderId >= 0 {
			return leaderId
		}
		time.Sleep(150 * time.Millisecond)
	}

	h.t.Fatalf("leader not found")
	return -1
}

// CheckPut отправляет через клиента c запрос Put и проверяет, что он
// завершился без ошибок. Возвращает (prevValue, keyFound).
func (h *Harness) CheckPut(c *kvclient.KVClient, key, value string) (string, bool) {
	ctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
	defer cancel()
	pv, f, err := c.Put(ctx, key, value)
	if err != nil {
		h.t.Error(err)
	}
	return pv, f
}

// CheckGet отправляет через клиента c запрос Get и проверяет отсутствие
// ошибок. Также проверяет, что ключ найден и его значение совпадает с
// ожидаемым.
func (h *Harness) CheckGet(c *kvclient.KVClient, key string, wantValue string) {
	ctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
	defer cancel()
	gv, f, err := c.Get(ctx, key)
	if err != nil {
		h.t.Error(err)
	}
	if !f {
		h.t.Errorf("got found=false, want true for key=%s", key)
	}
	if gv != wantValue {
		h.t.Errorf("got value=%v, want %v", gv, wantValue)
	}
}

// CheckCAS отправляет через клиента c запрос CAS и проверяет, что он
// завершился без ошибок. Возвращает (prevValue, keyFound).
func (h *Harness) CheckCAS(c *kvclient.KVClient, key, compare, value string) (string, bool) {
	ctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
	defer cancel()
	pv, f, err := c.CAS(ctx, key, compare, value)
	if err != nil {
		h.t.Error(err)
	}
	return pv, f
}

// CheckGetNotFound отправляет через клиента c запрос Get и проверяет
// отсутствие ошибок, а также то, что указанный ключ отсутствует в сервисе.
func (h *Harness) CheckGetNotFound(c *kvclient.KVClient, key string) {
	ctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
	defer cancel()
	_, f, err := c.Get(ctx, key)
	if err != nil {
		h.t.Error(err)
	}
	if f {
		h.t.Errorf("got found=true, want false for key=%s", key)
	}
}

// CheckGetTimesOut проверяет, что запрос Get, отправленный через данного
// клиента, завершится по тайм-ауту при использовании контекста с дедлайном,
// поскольку клиент не сможет добиться фиксации своей команды сервисом.
func (h *Harness) CheckGetTimesOut(c *kvclient.KVClient, key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err := c.Get(ctx, key)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		h.t.Errorf("got err %v; want 'deadline exceeded'", err)
	}
}

func tlog(format string, a ...any) {
	format = "[TEST] " + format
	log.Printf(format, a...)
}
