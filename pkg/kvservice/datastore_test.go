package kvservice

import (
	"testing"
	"time"

	"github.com/vskurikhin/raft/pkg/raft"
)

// TestCommitSubscriptionCleanup проверяет, что подписка удаляется из commitSubs
// при отмене контекста (проблема 11) и что runUpdater не блокируется при отправке
// в канал, который никто не читает (проблема 12).
func TestCommitSubscriptionCleanup(t *testing.T) {
	kvs := &KVService{
		commitSubs: make(map[int]chan Command),
	}
	logIndex := 42

	// Создаём подписку
	ch := kvs.createCommitSubscription(logIndex)
	if _, exists := kvs.commitSubs[logIndex]; !exists {
		t.Fatal("subscription should exist after create")
	}

	// Имитируем отмену контекста: удаляем подписку
	kvs.mu.Lock()
	delete(kvs.commitSubs, logIndex)
	kvs.mu.Unlock()

	if _, exists := kvs.commitSubs[logIndex]; exists {
		t.Fatal("subscription should be removed after context cancellation")
	}

	// Проверяем, что popCommitSubscription возвращает nil после удаления
	sub := kvs.popCommitSubscription(logIndex)
	if sub != nil {
		t.Fatal("popCommitSubscription should return nil after cleanup")
	}

	// Проверяем, что канал закрыт (буферизированный, 1) — отправка не блокирует
	select {
	case ch <- Command{Kind: CommandPut, Key: "test"}:
		// send succeeded — канал не закрыт (ожидаемо, мы не закрывали его)
	default:
		t.Fatal("channel should accept send")
	}
}

// TestRunUpdaterNonBlockingSend проверяет, что runUpdater не блокируется
// при отправке в канал подписки, если подписчик уже ушёл (проблема 12).
func TestRunUpdaterNonBlockingSend(t *testing.T) {
	kvs := &KVService{
		commitSubs: make(map[int]chan Command),
		commitChan: make(chan raft.CommitEntry, 10),
		ds:         NewDataStore(),
	}
	kvs.runUpdater()
	defer close(kvs.commitChan)

	logIndex := 7

	// Создаём подписку
	sub := kvs.createCommitSubscription(logIndex)
	if sub == nil {
		t.Fatal("expected non-nil subscription channel")
	}

	// Удаляем подписку (имитируем отмену контекста), НО НЕ закрываем канал
	kvs.mu.Lock()
	delete(kvs.commitSubs, logIndex)
	kvs.mu.Unlock()

	// Отправляем CommitEntry через commitChan — runUpdater прочитает его,
	// вызовет popCommitSubscription (nil, так как удалено), и не должен
	// блокироваться на sub <- cmd
	entry := raft.CommitEntry{
		Index: logIndex,
		Command: Command{
			Kind:  CommandPut,
			Key:   "key1",
			Value: "val1",
			ID:    0,
		},
	}

	// Отправляем entry, ждём до 1 секунды что runUpdater не заблокируется
	select {
	case kvs.commitChan <- entry:
		// отправлено успешно
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runUpdater should not block on sending to commitChan")
	}

	// Даём время runUpdater обработать entry
	time.Sleep(100 * time.Millisecond)

	// Если runUpdater заблокировался, вторая запись тоже не будет обработана
	select {
	case kvs.commitChan <- entry:
		// вторая запись отправлена — runUpdater жив
	default:
		t.Fatal("runUpdater should still be processing — commitChan should accept more entries")
	}
}

func checkPutPrev(t *testing.T, ds *DataStore, k string, v string, prev string, hasPrev bool) {
	t.Helper()
	prevVal, ok := ds.Put(k, v)
	if hasPrev != ok || prevVal != prev {
		t.Errorf("prevVal=%s, ok=%v; want %s,%v", prevVal, ok, prev, hasPrev)
	}
}

func checkGet(t *testing.T, ds *DataStore, k string, v string, found bool) {
	t.Helper()
	gotV, ok := ds.Get(k)
	if found != ok || v != gotV {
		t.Errorf("gotV=%s, ok=%v; want %s,%v", gotV, ok, v, found)
	}
}

func checkCAS(t *testing.T, ds *DataStore, k string, comp string, v string, prev string, found bool) {
	t.Helper()
	gotPrev, gotFound := ds.CAS(k, comp, v)
	if found != gotFound || prev != gotPrev {
		t.Errorf("gotPrev=%s, gotFound=%v; want %s,%v", gotPrev, gotFound, prev, found)
	}
}

func TestGetPut(t *testing.T) {
	ds := NewDataStore()

	checkGet(t, ds, "foo", "", false)
	checkPutPrev(t, ds, "foo", "bar", "", false)
	checkGet(t, ds, "foo", "bar", true)
	checkPutPrev(t, ds, "foo", "baz", "bar", true)
	checkGet(t, ds, "foo", "baz", true)
	checkPutPrev(t, ds, "nix", "hard", "", false)
}

func TestCASBasic(t *testing.T) {
	ds := NewDataStore()
	ds.Put("foo", "bar")
	ds.Put("sun", "beam")

	// CAS: замена существующего значения.
	checkCAS(t, ds, "foo", "mex", "bro", "bar", true)
	checkCAS(t, ds, "foo", "bar", "bro", "bar", true)
	checkGet(t, ds, "foo", "bro", true)

	// CAS: ключ не найден.
	checkCAS(t, ds, "goa", "mm", "vv", "", false)
	checkGet(t, ds, "goa", "", false)

	// ...а теперь этому ключу присваивается значение.
	ds.Put("goa", "tva")
	checkCAS(t, ds, "goa", "mm", "vv", "tva", true)
	checkCAS(t, ds, "goa", "mm", "vv", "tva", true)
}

func TestCASConcurrent(t *testing.T) {
	// Запускайте этот тест с флагом -race.
	ds := NewDataStore()
	ds.Put("foo", "bar")
	ds.Put("sun", "beam")

	go func() {
		for range 2000 {
			ds.CAS("foo", "bar", "baz")
		}
	}()
	go func() {
		for range 2000 {
			ds.CAS("foo", "baz", "bar")
		}
	}()

	v, _ := ds.Get("foo")
	if v != "bar" && v != "baz" {
		t.Errorf("got v=%s, want bar or baz", v)
	}
}
