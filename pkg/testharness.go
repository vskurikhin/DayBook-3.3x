package pkg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/api"
	"github.com/vskurikhin/raft/pkg/kvclient"
	"github.com/vskurikhin/raft/pkg/kvservice"
)

func init() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
}

// Именованные бюджеты и периоды опроса system-харнесса: выводятся из
// констант протокола, литеральные длительности в ожиданиях запрещены.
const (
	// pollInterval — период опроса примитивов ожидания (класс pollInterval
	// корневого харнесса, 10 мс).
	pollInterval = 10 * time.Millisecond

	// maxElectionTimeout — максимальный тайм-аут выборов:
	// 2*ReelectionTimeoutMs = 762 мс.
	maxElectionTimeout = 2 * raft.ReelectionTimeoutMs * time.Millisecond

	// maxReplicationBackoff — потолок задержки повторов репликации
	// (1000 мс). Дублирует не экспортированное значение пакета raft;
	// при рассинхроне источник истины — raft_cm_replication.go.
	maxReplicationBackoff = 1000 * time.Millisecond

	// singleLeaderBudget — бюджет схождения к единственному лидеру:
	// два worst-case выборов (2*maxElectionTimeout + раунд Pre-Vote)
	// плюс задержка step-down призрачного лидера прежнего терма,
	// ограниченная потолком задержки повторов: 3*762 + 1000 ≈ 3.3 с.
	singleLeaderBudget = 3*maxElectionTimeout + maxReplicationBackoff

	// serviceReadyBudget — бюджет ожидания готовности перезапущенного
	// сервиса (подъём HTTP-слушателя и Raft-узла новой инкарнации);
	// принят равным singleLeaderBudget: готовность не требует лидера,
	// поэтому запас относительно worst-case подъёма кратный.
	serviceReadyBudget = singleLeaderBudget

	// serviceProbeTimeout — тайм-аут одного HTTP-пробника готовности
	// (тот же порядок, что у клиентских запросов харнесса:
	// 600*Quantum = 1.8 с).
	serviceProbeTimeout = 600 * raft.Quantum * time.Millisecond

	// clientOpTimeout — тайм-аут контекста клиентских операций Put/Get
	// (600*Quantum = 1.8 с); тот же порядок величины, что у пробника
	// готовности сервиса (serviceProbeTimeout).
	clientOpTimeout = 600 * raft.Quantum * time.Millisecond

	// clientOpTimeoutCAS — тайм-аут контекста CAS (800*Quantum =
	// 2.4 с); увеличенный относительно Put/Get запас.
	clientOpTimeoutCAS = 800 * raft.Quantum * time.Millisecond

	// clientOpTimeoutProbe — тайм-аут контекста Get-пробников:
	// проверка отсутствия ключа и одиночный запрос в ожидании
	// значения (500*Quantum = 1.5 с).
	clientOpTimeoutProbe = 500 * raft.Quantum * time.Millisecond

	// clientOpTimeoutShort — короткий дедлайн Get в контроле истечения
	// тайм-аута клиентом (300*Quantum = 0.9 с): сервис с отключёнными
	// ответами не успевает зафиксировать команду за это время.
	clientOpTimeoutShort = 300 * raft.Quantum * time.Millisecond

	// waitKeyValueBudget — бюджет схождения значения ключа
	// в ожидании WaitForKeyValue (2 с).
	waitKeyValueBudget = 2 * time.Second
)

// Harness Тестовый стенд для системного тестирования kvservice и клиента.
//
// Владение состоянием: kvServiceAddrs, connected и
// alive защищены h.mu — адреса сервисов меняются при рестарте на :0
// (новый порт), поэтому читатели обязаны брать их под блокировкой.
// Инвариант границ: h.mu не удерживается через блокирующие
// операции жизненного цикла (Shutdown сервисов, ConnectToRaftPeer).
type Harness struct {
	mu sync.Mutex

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
		kvss[i] = kvservice.NewKVService(":0", i, peerIds, storage[i], ready)
		alive[i] = true
	}

	// Соединить Raft-узлы всех сервисов между собой и закрыть канал ready,
	// уведомив их о том, что кластер полностью готов к работе.
	for i := range n {
		for j := range n {
			if i != j {
				_ = kvss[i].ConnectToRaftPeer(j, kvss[j].GetRaftListenAddr())
			}
		}
		connected[i] = true
	}
	close(ready)

	// Каждый экземпляр KVService обслуживает REST API на своём TCP-порту.
	// Порт назначает ядро (":0"): фиксированные порты приводили к конфликтам
	// при параллельном запуске тестовых процессов.
	kvServiceAddrs := make([]string, n)
	for i := range n {
		if err := kvss[i].ServeHTTP(":0"); err != nil {
			t.Fatalf("ServeHTTP for service %d: %v", i, err)
		}
		kvServiceAddrs[i] = clientAddr(t, kvss[i])
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
			_ = h.kvCluster[j].DisconnectFromRaftPeer(id)
		}
	}
	h.mu.Lock()
	h.connected[id] = false
	h.mu.Unlock()
}

func (h *Harness) ReconnectServiceToPeers(id int) {
	tlog("Reconnect %d", id)
	alive := h.aliveSnapshot()
	for j := 0; j < h.n; j++ {
		if j != id && alive[j] {
			if err := h.kvCluster[id].ConnectToRaftPeer(j, h.kvCluster[j].GetRaftListenAddr()); err != nil {
				h.t.Fatal(err)
			}
			if err := h.kvCluster[j].ConnectToRaftPeer(id, h.kvCluster[id].GetRaftListenAddr()); err != nil {
				h.t.Fatal(err)
			}
		}
	}
	h.mu.Lock()
	h.connected[id] = true
	h.mu.Unlock()
}

// clientAddr переводит фактический адрес слушателя сервиса
// (":0" даёт вид "[::]:PORT" — wildcard, непригодный для набора)
// в адрес "localhost:PORT", по которому обращаются тестовые клиенты.
func clientAddr(t *testing.T, kvs *kvservice.KVService) string {
	t.Helper()
	addr := kvs.GetHTTPListenAddr()
	if addr == "" {
		t.Fatal("service has no HTTP listen address; ServeHTTP was not called")
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("cannot parse HTTP listen address %q: %v", addr, err)
	}
	return net.JoinHostPort("localhost", port)
}

// aliveSnapshot возвращает копию среза alive, снятую под h.mu.
// Контракт: вызывается БЕЗ удерживаемой h.mu.
func (h *Harness) aliveSnapshot() []bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]bool, h.n)
	copy(out, h.alive)
	return out
}

// connectedSnapshot возвращает копию среза connected, снятую под h.mu.
// Контракт: вызывается БЕЗ удерживаемой h.mu.
func (h *Harness) connectedSnapshot() []bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]bool, h.n)
	copy(out, h.connected)
	return out
}

// aliveAddrs возвращает копию адресов работающих сервисов, снятую под
// h.mu. Копия обязательна: срез h.kvServiceAddrs обновляется при
// рестарте сервиса на новом порту.
func (h *Harness) aliveAddrs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	addrs := make([]string, 0, h.n)
	for i := range h.n {
		if h.alive[i] {
			addrs = append(addrs, h.kvServiceAddrs[i])
		}
	}
	return addrs
}

// CrashService «аварийно завершает» работу сервиса, отключая его от всех
// соседних узлов, а затем корректно останавливая. Этот экземпляр сервиса
// больше не будет использоваться.
func (h *Harness) CrashService(id int) {
	tlog("Crash %d", id)
	h.DisconnectServiceFromPeers(id)
	h.mu.Lock()
	h.alive[id] = false
	h.mu.Unlock()
	if err := h.kvCluster[id].Shutdown(); err != nil {
		h.t.Errorf("error while shutting down service %d: %v", id, err)
	}
}

// RestartService «перезапускает» сервис, создавая новый экземпляр и
// подключая его к соседним узлам.
func (h *Harness) RestartService(id int) {
	if h.aliveSnapshot()[id] {
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
	h.kvCluster[id] = kvservice.NewKVService(":0", id, peerIds, h.storage[id], ready)
	// Рестарт на ":0" даёт НОВЫЙ порт: адрес сервиса обязан быть обновлён,
	// иначе клиенты, собранные из kvServiceAddrs, обращались бы к порту
	// прежней инкарнации.
	if err := h.kvCluster[id].ServeHTTP(":0"); err != nil {
		h.t.Fatalf("ServeHTTP for restarted service %d: %v", id, err)
	}
	addr := clientAddr(h.t, h.kvCluster[id])

	h.ReconnectServiceToPeers(id)
	close(ready)
	h.mu.Lock()
	h.kvServiceAddrs[id] = addr
	h.alive[id] = true
	h.mu.Unlock()
	// replace: фиксированная пауза заменена опросом наблюдаемого признака
	// готовности — завершённый round-trip через /verifyleader/ доказывает
	// одновременно обслуживание HTTP и отзывчивость Raft-узла.
	h.waitForServiceReady(id)
}

// waitForServiceReady ждёт готовности перезапущенного сервиса по
// наблюдаемому признаку R1 ∧ R2 ∧ R3:
//
//   - R1: GetHTTPListenAddr() != "" и выведенный из него localhost:<port>
//     совпадает с h.kvServiceAddrs[id] (адрес инкарнации обновлён);
//   - R2: GetRaftListenAddr() != nil (слушатель Raft-транспорта открыт);
//   - R3: POST /verifyleader/ возвращает 200 и тело, декодируемое
//     в api.StatusResponse со статусом StatusOK либо StatusNotLeader —
//     обработчик проходит через VerifyLeader, поэтому завершённый
//     round-trip доказывает и обслуживание HTTP, и отзывчивость
//     Raft-узла. Оба статуса допустимы: роль узла к готовности
//     отношения не имеет. Не-готовность — только отказ соединения,
//     EOF, истечение тайм-аута запроса и нераспознанный статус.
//
// Пробник выполняется напрямую через net/http (клиент из одного адреса
// зацикливается при StatusNotLeader). Контракт НЕ гарантирует, что узел
// вернулся в кворум или что в кластере есть лидер: для этого служат
// CheckSingleLeader/WaitForSingleLeader и WaitForKeyValue.
func (h *Harness) waitForServiceReady(id int) {
	h.t.Helper()
	deadline := time.Now().Add(serviceReadyBudget)
	for time.Now().Before(deadline) {
		if h.serviceReady(id) {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(pollInterval)
	}
	h.t.Fatalf("restarted service %d did not become ready within %v\n  %s",
		id, serviceReadyBudget, h.serviceStates())
}

// serviceReady вычисляет признак готовности R1 ∧ R2 ∧ R3 (см.
// waitForServiceReady). Не обращается к *testing.T — допустимо в любом
// контексте.
func (h *Harness) serviceReady(id int) bool {
	kvs := h.kvCluster[id]

	// R1: адрес HTTP-слушателя обновлён и совпадает с адресом инкарнации.
	addr := kvs.GetHTTPListenAddr()
	if addr == "" {
		return false
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	h.mu.Lock()
	want := h.kvServiceAddrs[id]
	h.mu.Unlock()
	derived := net.JoinHostPort("localhost", port)
	if derived != want {
		return false
	}

	// R2: слушатель Raft-транспорта новой инкарнации открыт.
	if kvs.GetRaftListenAddr() == nil {
		return false
	}

	// R3: завершённый round-trip через существующий эндпоинт.
	ctx, cancel := context.WithTimeout(h.ctx, serviceProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+want+"/verifyleader/", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var sr api.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return false
	}
	return sr.RespStatus == api.StatusOK || sr.RespStatus == api.StatusNotLeader
}

// serviceStates возвращает строку состояния всех сервисов (id, IsLeader,
// connected, alive, HTTP-адрес) для диагностики при исчерпании бюджета.
func (h *Harness) serviceStates() string {
	h.mu.Lock()
	connected := make([]bool, h.n)
	alive := make([]bool, h.n)
	copy(connected, h.connected)
	copy(alive, h.alive)
	h.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("state:")
	for i := range h.n {
		leader := h.kvCluster[i].IsLeader()
		h.mu.Lock()
		addr := h.kvServiceAddrs[i]
		h.mu.Unlock()
		_, _ = fmt.Fprintf(&sb, " [%d: isLeader=%t connected=%t alive=%t http=%s]",
			i, leader, connected[i], alive[i], addr)
	}
	return sb.String()
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
		h.mu.Lock()
		h.connected[i] = false
		h.mu.Unlock()
	}

	// Эти вызовы помогают HTTP-серверу внутри KVService корректно завершить работу.
	http.DefaultClient.CloseIdleConnections()
	h.ctxCancel()

	for i := range h.n {
		// Короткая секция h.mu: сам Shutdown сервиса выполняется вне её.
		h.mu.Lock()
		wasAlive := h.alive[i]
		h.alive[i] = false
		h.mu.Unlock()

		if wasAlive {
			if err := h.kvCluster[i].Shutdown(); err != nil {
				h.t.Errorf("error while shutting down service %d: %v", i, err)
			}
		}
	}
}

// NewClient создает нового клиента, который будет обращаться ко всем
// существующим работающим сервисам.
func (h *Harness) NewClient() *kvclient.KVClient {
	return kvclient.New(h.aliveAddrs())
}

// NewClientWithRandomAddrsOrder создает нового клиента, который будет
// обращаться ко всем существующим работающим сервисам, но в случайном
// порядке адресов.
func (h *Harness) NewClientWithRandomAddrsOrder() *kvclient.KVClient {
	addrs := h.aliveAddrs()
	// keep: легитимная рандомизация порядка адресов — предмет теста
	// (клиент обязан находить лидера при любом порядке). Инъекция
	// детерминированного RNG отклонена.
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
	// Копия, а не срез поверх общего массива: aliasing позволял клиенту
	// увидеть адрес другой инкарнации после RestartService.
	h.mu.Lock()
	addrs := []string{h.kvServiceAddrs[id]}
	h.mu.Unlock()
	return kvclient.New(addrs)
}

// ServiceAddr возвращает текущий HTTP-адрес сервиса id (под h.mu:
// адрес меняется при RestartService на ":0").
func (h *Harness) ServiceAddr(id int) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.kvServiceAddrs[id]
}

// CheckSingleLeader проверяет схождение кластера к ровно одному лидеру
// и возвращает идентификатор этого сервиса. Толерантная форма без термов
// (термы в публичный API не выведены): множественность лидеров —
// переходное состояние, изолированный лидер прежнего терма уходит в
// step-down с задержкой повторов репликации, поэтому фатальный отказ
// выполняется только по исчерпании именованного бюджета. Утверждение на
// этом уровне слабее, чем на уровне модульных тестов корневого пакета
// (Election Safety с термами): расхождение сознательное.
func (h *Harness) CheckSingleLeader() int {
	h.t.Helper()
	return h.WaitForSingleLeader(singleLeaderBudget)
}

// Try*/Check* — две формы одних и тех же проверок.
//
// Try*-варианты возвращают ошибку и НЕ обращаются к *testing.T: только
// они допустимы внутри worker-горутин (t.Error*/t.Fatal* из горутины
// после конца теста — гонка и паника). Check*-варианты — тонкие обёртки,
// репортящие ту же ошибку через h.t; они вызываются только из тестовой
// горутины.

// TryPut отправляет через клиента c запрос Put. Возвращает
// (prevValue, keyFound, error) без обращения к *testing.T.
func (h *Harness) TryPut(c *kvclient.KVClient, key, value string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(h.ctx, clientOpTimeout)
	defer cancel()
	return c.Put(ctx, key, value)
}

// TryGet отправляет через клиента c запрос Get и проверяет, что ключ
// найден и его значение совпадает с ожидаемым. Возвращает ошибку
// без обращения к *testing.T.
func (h *Harness) TryGet(c *kvclient.KVClient, key string, wantValue string) error {
	ctx, cancel := context.WithTimeout(h.ctx, clientOpTimeout)
	defer cancel()
	gv, f, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	if !f {
		return fmt.Errorf("got found=false, want true for key=%s", key)
	}
	if gv != wantValue {
		return fmt.Errorf("got value=%v, want %v for key=%s", gv, wantValue, key)
	}
	return nil
}

// TryCAS отправляет через клиента c запрос CAS. Возвращает
// (prevValue, keyFound, error) без обращения к *testing.T.
func (h *Harness) TryCAS(c *kvclient.KVClient, key, compare, value string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(h.ctx, clientOpTimeoutCAS)
	defer cancel()
	return c.CAS(ctx, key, compare, value)
}

// TryGetNotFound отправляет через клиента c запрос Get и проверяет,
// что ключ отсутствует. Возвращает ошибку без обращения к *testing.T.
func (h *Harness) TryGetNotFound(c *kvclient.KVClient, key string) error {
	ctx, cancel := context.WithTimeout(h.ctx, clientOpTimeoutProbe)
	defer cancel()
	_, f, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	if f {
		return fmt.Errorf("got found=true, want false for key=%s", key)
	}
	return nil
}

// WaitForSingleLeader ждёт, пока среди подключённых сервисов останется
// ровно один лидер, и возвращает его ID.
//
// Окно «два лидера» после переподключения изолированного сервиса не
// является ошибкой: прежний лидер узнаёт о более высоком терме только
// с первым дошедшим AppendEntries, доставка которого откладывается
// задержкой повторов репликации до потолка повторов. Это единственная
// реализация цикла опроса — CheckSingleLeader выражен через неё
// с бюджетом по умолчанию.
func (h *Harness) WaitForSingleLeader(timeout time.Duration) int {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		connected := h.connectedSnapshot()
		leaderId, multiple := -1, false
		for i := range h.n {
			if !connected[i] || !h.kvCluster[i].IsLeader() {
				continue
			}
			if leaderId >= 0 {
				multiple = true
				break
			}
			leaderId = i
		}
		if !multiple && leaderId >= 0 {
			return leaderId
		}
		if !time.Now().Before(deadline) {
			break
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(pollInterval)
	}
	h.t.Fatalf(
		"no single leader within budget %v (polled %v):\n"+
			"  expected: exactly one leader among connected services\n  %s",
		timeout, pollInterval, h.serviceStates(),
	)
	return -1
}

// CheckPut отправляет через клиента c запрос Put и проверяет, что он
// завершился без ошибок. Возвращает (prevValue, keyFound).
// Вызывается только из тестовой горутины (см. TryPut).
func (h *Harness) CheckPut(c *kvclient.KVClient, key, value string) (string, bool) {
	h.t.Helper()
	pv, f, err := h.TryPut(c, key, value)
	if err != nil {
		h.t.Error(err)
	}
	return pv, f
}

// CheckGet отправляет через клиента c запрос Get и проверяет отсутствие
// ошибок. Также проверяет, что ключ найден и его значение совпадает с
// ожидаемым. Вызывается только из тестовой горутины (см. TryGet).
func (h *Harness) CheckGet(c *kvclient.KVClient, key string, wantValue string) {
	h.t.Helper()
	if err := h.TryGet(c, key, wantValue); err != nil {
		h.t.Error(err)
	}
}

// CheckCAS отправляет через клиента c запрос CAS и проверяет, что он
// завершился без ошибок. Возвращает (prevValue, keyFound).
// Вызывается только из тестовой горутины (см. TryCAS).
func (h *Harness) CheckCAS(c *kvclient.KVClient, key, compare, value string) (string, bool) {
	h.t.Helper()
	pv, f, err := h.TryCAS(c, key, compare, value)
	if err != nil {
		h.t.Error(err)
	}
	return pv, f
}

// CheckGetNotFound отправляет через клиента c запрос Get и проверяет
// отсутствие ошибок, а также то, что указанный ключ отсутствует в сервисе.
// Вызывается только из тестовой горутины (см. TryGetNotFound).
func (h *Harness) CheckGetNotFound(c *kvclient.KVClient, key string) {
	h.t.Helper()
	if err := h.TryGetNotFound(c, key); err != nil {
		h.t.Error(err)
	}
}

// CheckGetTimesOut проверяет, что запрос Get, отправленный через данного
// клиента, завершится по тайм-ауту при использовании контекста с дедлайном,
// поскольку клиент не сможет добиться фиксации своей команды сервисом.
func (h *Harness) CheckGetTimesOut(c *kvclient.KVClient, key string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), clientOpTimeoutShort)
	defer cancel()
	_, _, err := c.Get(ctx, key)
	if err == nil {
		h.t.Error("got err nil; want an error wrapping context.DeadlineExceeded")
		return
	}
	// Проверка по фактической цепочке ошибок, а не по подстроке
	// kvclient.Get → send → sendJSONRequest →
	// http.Client.Do возвращает *url.Error, оборачивающий
	// context.DeadlineExceeded, и send возвращает его как есть при
	// ctx.Err() != nil. Ветка «commit failed; please retry»
	// (kvclient StatusFailedCommit) контекст НЕ оборачивает — если
	// сценарий уйдёт в неё, тест обязан упасть, а не молча пройти.
	if !errors.Is(err, context.DeadlineExceeded) {
		h.t.Errorf("got err %v (%T); want an error wrapping context.DeadlineExceeded", err, err)
	}
}

// WaitForKeyValue ждёт, пока Get(key) не вернется с ожидаемым значением
// expectedValue или до истечения таймаута 2s. При таймауте вызывает
// h.CheckGet, который завершит тест с Fatal.
func (h *Harness) WaitForKeyValue(c *kvclient.KVClient, key, expectedValue string) {
	h.t.Helper()
	deadline := time.Now().Add(waitKeyValueBudget)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(h.ctx, clientOpTimeoutProbe)
		val, _, err := c.Get(ctx, key)
		cancel()
		if err == nil && val == expectedValue {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(20 * time.Millisecond)
	}
	h.CheckGet(c, key, expectedValue)
}

func tlog(format string, a ...any) {
	format = "[TEST] " + format
	log.Printf(format, a...)
}
