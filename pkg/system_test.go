package pkg

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
)

func TestSetupHarness(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()
	// replace: предмет теста — что кластер поднимается и выбирает лидера;
	// проверяется наблюдаемым состоянием, а не паузой.
	h.CheckSingleLeader()
}

func TestClientRequestBeforeConsensus(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()
	// remove: предмет теста — обращение клиента ДО завершения выборов;
	// пауза здесь ослабляла бы его. Клиент последовательно обходит все
	// сервисы, пока не найдёт лидера.
	c1 := h.NewClient()
	h.CheckPut(c1, "llave", "cosa")
	// remove: завершающая пауза ничего не проверяет; остановку сервисов
	// выполняет h.Shutdown().
}

func TestBasicPutGetSingleClient(t *testing.T) {
	// Базовая проверка: отправить один Put, затем один Get от одного клиента.
	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	h.CheckPut(c1, "llave", "cosa")

	h.CheckGet(c1, "llave", "cosa")
	// remove: завершающая пауза ничего не проверяет.
}

func TestPutPrevValue(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	// Проверяем, что до и после Put возвращаются ожидаемые значения found и prev.
	prev, found := h.CheckPut(c1, "llave", "cosa")
	if found || prev != "" {
		t.Errorf(`got found=%v, prev=%v, want false/""`, found, prev)
	}

	prev, found = h.CheckPut(c1, "llave", "frodo")
	if !found || prev != "cosa" {
		t.Errorf(`got found=%v, prev=%v, want true/"cosa"`, found, prev)
	}

	// Другой ключ...
	prev, found = h.CheckPut(c1, "mafteah", "davar")
	if found || prev != "" {
		t.Errorf(`got found=%v, prev=%v, want false/""`, found, prev)
	}
}

func TestBasicPutGetDifferentClients(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	h.CheckPut(c1, "k", "v")

	c2 := h.NewClient()
	h.CheckGet(c2, "k", "v")
	// remove: завершающая пауза ничего не проверяет.
}

func TestCASBasic(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	h.CheckPut(c1, "k", "v")

	if pv, found := h.CheckCAS(c1, "k", "v", "newv"); pv != "v" || !found {
		t.Errorf("got %s,%v, want replacement", pv, found)
	}
}

func TestCASConcurrent(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()
	c := h.NewClient()
	h.CheckPut(c, "foo", "mexico")

	// Worker использует Try*-хелперы и не обращается к *testing.T;
	// ошибки доставляются через errCh и репортятся после wg.Wait().
	// defer wg.Wait() зарегистрирован ПОСЛЕ defer h.Shutdown(), поэтому
	// выполняется раньше него (join → Shutdown → leaktest).
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	started := make(chan struct{}, 1)
	defer wg.Wait()

	wg.Add(1)
	go func() {
		defer wg.Done()
		c := h.NewClient()
		for {
			pv, _, err := h.TryCAS(c, "foo", "bar", "bomba")
			// Сигнал «первая попытка CAS выполнена» — заменяет
			// фиксированную паузу перед конкурирующим PUT.
			select {
			case started <- struct{}{}:
			default:
			}
			if err != nil {
				errCh <- err
				return
			}
			if pv == "bar" {
				return
			}
			select {
			case <-h.ctx.Done():
				return
			default:
			}
		}
	}()

	// replace: ждём наблюдаемого события (worker выполнил первый CAS)
	// вместо фиксированной паузы.
	<-started
	c2 := h.NewClient()
	h.CheckPut(c2, "foo", "bar")

	h.WaitForKeyValue(c2, "foo", "bomba")

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestConcurrentClientsPutsAndGets(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	// Проверить, что несколько запросов PUT и GET могут выполняться
	// одновременно, когда каждая горутина отправляет свой запрос параллельно.
	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	runConcurrentPutsAndGets(t, h, 9)
}

func Test5ServerConcurrentClientsPutsAndGets(t *testing.T) {
	// Аналогично предыдущему тесту, но используется кластер Raft
	// из пяти серверов.
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 5)
	defer h.Shutdown()
	h.CheckSingleLeader()

	runConcurrentPutsAndGets(t, h, 9)
}

// runConcurrentPutsAndGets выполняет две фазы параллельных запросов
// (PUT, затем GET) по паттерну:
// start → work → error propagation (errCh) → Wait → assertions.
//
// Worker-горутины используют Try*-хелперы и НЕ обращаются к *testing.T;
// ошибки собираются в буферизованный канал и репортятся тестовой
// горутиной после wg.Wait(). Join зарегистрирован через defer внутри
// хелпера — он отрабатывает и при раннем t.Fatal, причём ДО
// defer h.Shutdown() вызывающего теста (join → Shutdown → leaktest).
func runConcurrentPutsAndGets(t *testing.T, h *Harness, n int) {
	t.Helper()

	var wg sync.WaitGroup
	errCh := make(chan error, 2*n)
	// Join worker'ов гарантирован и при раннем t.Fatal: defer в этом
	// хелпере отрабатывает ДО defer h.Shutdown()/leaktest вызывающего
	// теста (порядок join → Shutdown → leaktest). t.Cleanup здесь
	// непригоден: cleanup-функции выполняются ПОСЛЕ всех defer теста.
	defer wg.Wait()

	// Фаза 1: параллельные PUT.
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := h.NewClient()
			_, f, err := h.TryPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
			if err != nil {
				errCh <- err
				return
			}
			if f {
				errCh <- fmt.Errorf("got key found for %d, want false", i)
			}
		}()
	}
	// Вместо sleepMs(150): фаза GET начинается после завершения всех PUT.
	wg.Wait()

	// Фаза 2: параллельные GET.
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := h.NewClient()
			if err := h.TryGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i)); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()

	// Assertions — после Wait, из тестовой горутины.
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestDisconnectLeaderAfterPuts(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()

	// Отправить несколько команд PUT.
	n := 4
	for i := range n {
		c := h.NewClient()
		h.CheckPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
	}

	h.DisconnectServiceFromPeers(lid)
	// replace: CheckSingleLeader сам опрашивает состояние до появления
	// единственного лидера — фиксированная пауза не нужна. Отключённый
	// лидер исключён из опроса (connected[lid] == false).
	newlid := h.CheckSingleLeader()

	if newlid == lid {
		t.Errorf("got the same leader")
	}

	// Попытка обратиться к отключённому лидеру должна завершиться по тайм-ауту.
	c := h.NewClientSingleService(lid)
	h.CheckGetTimesOut(c, "key1")

	// Команды GET должны вернуть корректные значения.
	for range 5 {
		c := h.NewClientWithRandomAddrsOrder()
		for j := range n {
			h.CheckGet(c, fmt.Sprintf("key%v", j), fmt.Sprintf("value%v", j))
		}
	}

	// Возвращаем изолированный сервис в кластер и дожидаемся схождения
	// к единственному лидеру. Прежняя схема (реконнект + sleepMs(200)
	// с комментарием «утечка одной горутины не критична») допускала
	// утечку; теперь h.Shutdown() в defer полностью останавливает все
	// сервисы, поэтому непогашенных горутин не остаётся.
	h.ReconnectServiceToPeers(lid)
	h.WaitForSingleLeader(3 * time.Second)
}

func TestDisconnectLeaderAndFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()

	n := 4
	for i := range n {
		c := h.NewClient()
		_, f := h.CheckPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
		if f {
			t.Errorf("got key found for %d, want false", i)
		}
	}

	h.DisconnectServiceFromPeers(lid)
	otherId := (lid + 1) % 3
	h.DisconnectServiceFromPeers(otherId)

	c := h.NewClient()
	for r := 0; r < 10; r++ {
		ctx, cancel := context.WithTimeout(context.Background(), 300*raft.Quantum*time.Millisecond)
		_, _, err := c.Get(ctx, "key0")
		cancel()
		if err != nil {
			break
		}
		// poll-интервал condition-wait: ждём, пока потеря кворума
		// станет наблюдаемой (Get начнёт завершаться ошибкой).
		time.Sleep(50 * time.Millisecond)
	}
	h.CheckGetTimesOut(c, "key0")

	h.ReconnectServiceToPeers(otherId)
	h.ReconnectServiceToPeers(lid)
	h.CheckSingleLeader()
	for i := range n {
		h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
	}
}

func TestCrashFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()

	// Отправить несколько команд PUT.
	n := 3
	for i := range n {
		c := h.NewClient()
		_, f := h.CheckPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
		if f {
			t.Errorf("got key found for %d, want false", i)
		}
	}

	// Завершить работу одного из ведомых.
	otherId := (lid + 1) % 3
	h.CrashService(otherId)

	// Обращение напрямую к лидеру должно продолжать работать...
	for i := range n {
		c := h.NewClientSingleService(lid)
		h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
	}

	// Обращение к оставшимся работающим серверам также должно работать.
	for i := range n {
		c := h.NewClient()
		h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
	}
}

func TestCrashLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()

	// Отправить несколько команд PUT.
	n := 3
	for i := range n {
		c := h.NewClient()
		_, f := h.CheckPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
		if f {
			t.Errorf("got key found for %d, want false", i)
		}
	}

	// Завершить работу лидера и дождаться выбора нового лидера.
	h.CrashService(lid)
	h.CheckSingleLeader()

	// Обращение к оставшимся работающим серверам должно возвращать корректные данные.
	for i := range n {
		c := h.NewClient()
		h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
	}
}

func TestCrashThenRestartLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()

	// Отправить несколько команд PUT.
	n := 3
	for i := range n {
		c := h.NewClient()
		_, f := h.CheckPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
		if f {
			t.Errorf("got key found for %d, want false", i)
		}
	}

	// Завершить работу лидера и дождаться, пока кластер выберет нового лидера.
	h.CrashService(lid)
	h.CheckSingleLeader()

	// Обращение к оставшимся работающим серверам должно возвращать корректные данные.
	for i := range n {
		c := h.NewClient()
		h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
	}

	// Теперь перезапустить прежнего лидера: он присоединится к кластеру
	// и синхронизирует все данные.
	h.RestartService(lid)

	// Получить данные, обращаясь к сервисам в разном порядке.
	for range 5 {
		c := h.NewClientWithRandomAddrsOrder()
		for j := range n {
			h.CheckGet(c, fmt.Sprintf("key%v", j), fmt.Sprintf("value%v", j))
		}
	}
}

// TestRestartServiceGetsNewPort — регрессионный тест
// сервис слушает на ":0", поэтому после рестарта он получает
// НОВЫЙ порт. Харнесс обязан обновить kvServiceAddrs; перезапущенный
// сервис обязан отвечать по новому адресу, а клиенты, пересозданные
// после рестарта, — успешно выполнять put/get.
func TestRestartServiceGetsNewPort(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()

	c := h.NewClient()
	h.CheckPut(c, "restartkey", "restartvalue")

	// Останавливаем не-лидера: кластер сохраняет кворум и лидера.
	victim := (lid + 1) % 3
	oldAddr := h.ServiceAddr(victim)

	h.CrashService(victim)
	h.CheckSingleLeader()

	h.RestartService(victim)

	newAddr := h.ServiceAddr(victim)
	if newAddr == "" {
		t.Fatalf("service %d has no HTTP address after restart", victim)
	}
	if newAddr == oldAddr {
		t.Fatalf("service %d reused address %q after restart on \":0\"", victim, newAddr)
	}

	// Перезапущенный сервис отвечает по НОВОМУ адресу: /verifyleader/
	// возвращает 200 и на follower'е (в теле — StatusNotLeader).
	checkServiceReachable(t, newAddr)

	// Клиенты, пересозданные из обновлённого kvServiceAddrs, работают.
	h.WaitForKeyValue(h.NewClient(), "restartkey", "restartvalue")
	h.CheckPut(h.NewClient(), "restartkey2", "restartvalue2")
	h.WaitForKeyValue(h.NewClientWithRandomAddrsOrder(), "restartkey2", "restartvalue2")
}

// checkServiceReachable проверяет, что по адресу addr отвечает
// HTTP-обработчик KVService (эндпоинт /verifyleader/ не требует тела
// и отвечает как на лидере, так и на follower'е).
func checkServiceReachable(t *testing.T, addr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 600*raft.Quantum*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, "http://"+addr+"/verifyleader/", http.NoBody,
	)
	if err != nil {
		t.Fatalf("cannot build request for %s: %v", addr, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("service at %s is not reachable: %v", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("service at %s returned status %d, want %d", addr, resp.StatusCode, http.StatusOK)
	}
}
