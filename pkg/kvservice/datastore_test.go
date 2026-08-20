package kvservice

import (
	"fmt"
	"testing"
)

// checkDataStoreSnapshotRestore проверяет, что Snapshot/Restore корректно
// сохраняет и восстанавливает состояние DataStore.
func checkDataStoreSnapshotRestore(t *testing.T, ds *DataStore) {
	t.Helper()

	// Создаём снимок (сериализованные байты).
	data, err := ds.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	// Восстанавливаем новый DataStore из снимка.
	ds2 := NewDataStore()
	if err := ds2.Restore(data); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Проверяем, что все ключи совпадают.
	for k, v := range ds.data {
		if ds2.data[k] != v {
			t.Fatalf("key %q: original=%q, restored=%q", k, v, ds2.data[k])
		}
	}
	if len(ds.data) != len(ds2.data) {
		t.Fatalf("key count: original=%d, restored=%d", len(ds.data), len(ds2.data))
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

// TestDataStoreSnapshotRestore_Empty проверяет Snapshot/Restore для пустого DataStore.
func TestDataStoreSnapshotRestore_Empty(t *testing.T) {
	ds := NewDataStore()
	checkDataStoreSnapshotRestore(t, ds)
}

// TestDataStoreSnapshotRestore_WithData проверяет Snapshot/Restore для DataStore с данными.
func TestDataStoreSnapshotRestore_WithData(t *testing.T) {
	ds := NewDataStore()
	ds.Put("foo", "bar")
	ds.Put("baz", "qux")
	ds.Put("hello", "world")
	checkDataStoreSnapshotRestore(t, ds)
}

// TestDataStoreSnapshotRestore_LargeDataset проверяет Snapshot/Restore для большого набора.
func TestDataStoreSnapshotRestore_LargeDataset(t *testing.T) {
	ds := NewDataStore()
	for i := range 1000 {
		key := fmt.Sprintf("key%d", i)
		val := fmt.Sprintf("val%d", i)
		ds.Put(key, val)
	}
	checkDataStoreSnapshotRestore(t, ds)
}
