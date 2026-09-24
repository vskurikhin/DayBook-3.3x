package store

import "testing"

// TestMapStorage_GetReturnsCopy проверяет, что срез, возвращённый Get,
// является защитной копией: его мутация не видна повторному Get.
func TestMapStorage_GetReturnsCopy(t *testing.T) {
	ms := NewMapStorage()
	ms.Set("k", []byte{1, 2, 3})

	first, ok := ms.Get("k")
	if !ok {
		t.Fatal("expected value to be present")
	}
	first[0] = 99

	second, ok := ms.Get("k")
	if !ok {
		t.Fatal("expected value to be present")
	}
	if second[0] != 1 {
		t.Fatalf("Get returned mutated data: %v", second)
	}
}

// TestMapStorage_SetCopiesValue проверяет, что Set сохраняет копию
// переданного среза: его мутация после вызова не видна Get.
func TestMapStorage_SetCopiesValue(t *testing.T) {
	ms := NewMapStorage()
	value := []byte{1, 2, 3}
	ms.Set("k", value)

	value[0] = 99

	got, ok := ms.Get("k")
	if !ok {
		t.Fatal("expected value to be present")
	}
	if got[0] != 1 {
		t.Fatalf("Set stored caller-owned data: %v", got)
	}
}
