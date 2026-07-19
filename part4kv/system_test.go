package pkg

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

func sleepMs(n int) {
	time.Sleep(time.Duration(n) * time.Millisecond)
}

func TestSetupHarness(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()
	sleepMs(80)
}

func TestClientRequestBeforeConsensus(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()
	sleepMs(10)

	// Клиент будет последовательно обращаться ко всем сервисам,
	// пока не найдёт лидера.
	c1 := h.NewClient()
	h.CheckPut(c1, "llave", "cosa")
	sleepMs(80)
}

func TestBasicPutGetSingleClient(t *testing.T) {
	// Базовая проверка: отправить один Put, затем один Get от одного клиента.
	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	h.CheckPut(c1, "llave", "cosa")

	h.CheckGet(c1, "llave", "cosa")
	sleepMs(80)
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
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	h.CheckPut(c1, "k", "v")

	c2 := h.NewClient()
	h.CheckGet(c2, "k", "v")
	sleepMs(80)
}

func TestCASBasic(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

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
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()
	c := h.NewClient()
	h.CheckPut(c, "foo", "mexico")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c := h.NewClient()
		for range 20 {
			h.CheckCAS(c, "foo", "bar", "bomba")
		}
	}()

	// После того как клиент находит правильного лидера, полный цикл обработки
	// команды занимает 4–5 мс. В течение первых 50 мс после запуска горутин CAS
	// значение 'foo' ещё неверное, поэтому операция CAS не срабатывает, но
	sleepMs(50)
	c2 := h.NewClient()
	h.CheckPut(c2, "foo", "bar")

	sleepMs(300)
	h.CheckGet(c2, "foo", "bomba")

	wg.Wait()
}

func TestConcurrentClientsPutsAndGets(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

	// Проверить, что несколько запросов PUT и GET могут выполняться
	// одновременно, когда каждая горутина отправляет свой запрос параллельно.
	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	n := 9
	for i := range n {
		go func() {
			c := h.NewClient()
			_, f := h.CheckPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
			if f {
				t.Errorf("got key found for %d, want false", i)
			}
		}()
	}
	sleepMs(150)

	for i := range n {
		go func() {
			c := h.NewClient()
			h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
		}()
	}
	sleepMs(150)
}

func Test5ServerConcurrentClientsPutsAndGets(t *testing.T) {
	// Аналогично предыдущему тесту, но используется кластер Raft
	// из пяти серверов.
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

	h := NewHarness(t, 5)
	defer h.Shutdown()
	h.CheckSingleLeader()

	n := 9
	for i := range n {
		go func() {
			c := h.NewClient()
			_, f := h.CheckPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
			if f {
				t.Errorf("got key found for %d, want false", i)
			}
		}()
	}
	sleepMs(150)

	for i := range n {
		go func() {
			c := h.NewClient()
			h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
		}()
	}
	sleepMs(150)
}

func TestDisconnectLeaderAfterPuts(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

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
	sleepMs(300)
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

	// В конце теста повторно подключаем серверы, чтобы избежать утечки горутин.
	// В реальной системе предполагается, что сервисы рано или поздно будут
	// переподключены, а если нет — утечка одной горутины не критична, поскольку
	// сервер всё равно будет завершён.
	h.ReconnectServiceToPeers(lid)
	sleepMs(200)
}

func TestDisconnectLeaderAndFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()

	// Отправить несколько команд PUT.
	n := 4
	for i := range n {
		c := h.NewClient()
		_, f := h.CheckPut(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
		if f {
			t.Errorf("got key found for %d, want false", i)
		}
	}

	// Отключить лидера и ещё один сервер. Кластер потеряет кворум,
	// и клиентские запросы должны завершаться по тайм-ауту.
	h.DisconnectServiceFromPeers(lid)
	otherId := (lid + 1) % 3
	h.DisconnectServiceFromPeers(otherId)
	sleepMs(100)

	c := h.NewClient()
	h.CheckGetTimesOut(c, "key0")

	// Подключить обратно один сервер, но не старого лидера.
	// Все данные должны оставаться доступными.
	h.ReconnectServiceToPeers(otherId)
	h.CheckSingleLeader()
	for i := range n {
		h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
	}

	// Подключить обратно старого лидера.
	// Все данные по-прежнему должны быть доступны.
	h.ReconnectServiceToPeers(lid)
	h.CheckSingleLeader()
	for i := range n {
		h.CheckGet(c, fmt.Sprintf("key%v", i), fmt.Sprintf("value%v", i))
	}
	sleepMs(100)
}

func TestCrashFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

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
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

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
	defer leaktest.CheckTimeout(t, 100*time.Millisecond)()

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
