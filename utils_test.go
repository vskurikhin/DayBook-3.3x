package raft

import "testing"

// TestIsNilInterface проверяет функцию isNilInterface, используемую в
// предусловии конструктора NewConsensusModule.
//
// Ключевой сценарий — типизированный nil-указатель, положенный в интерфейс.
// В Go такой интерфейс НЕ равен nil: у него есть тип (например,
// *InmemTransport), а значением является nil-указатель. Прямое сравнение
// v == nil такую ситуацию НЕ обнаружило бы, и позже это привело бы к
// nil-dereference в месте использования интерфейса (например, в горутине
// leaderSendAEsToPeer). isNilInterface использует рефлексию и обязана
// вернуть true для этого случая.
//
// Сценарии:
//  1. «Чистый» nil-интерфейс (ничего не передано) — true.
//  2. Типизированный nil-указатель (*InmemTransport)(nil) в интерфейсе
//     Transport — true (главный случай, защищающий конструктор).
//  3. Ненулевой указатель на реальный транспорт — false.
//  4. Ссылочные типы с nil-значением (map, chan, slice, func) — true,
//     так как nil-значение в них также является типизированным nil.
func TestIsNilInterface(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want bool
	}{
		{
			// Ничего не передано — интерфейс пуст (тип и значение отсутствуют).
			name: "nil interface",
			v:    nil,
			want: true,
		},
		{
			// Ключевой случай: типизированный nil-указатель в интерфейсе.
			// Конструктор обязан его обнаружить, иначе горутина репликации
			// упадёт с nil-dereference при первом вызове AppendEntries.
			name: "typed nil pointer in interface",
			v:    Transport((*InmemTransport)(nil)),
			want: true,
		},
		{
			// Реальный ненулевой объект транспорта — предусловие выполняется.
			name: "non-nil pointer",
			v:    Transport(NewInmemTransport("test")),
			want: false,
		},
		{
			// nil-map: рефлексия обязана вернуть true через rv.IsNil().
			name: "nil map",
			v:    (map[string]int)(nil),
			want: true,
		},
		{
			// nil-канал: тоже ссылочный тип, рефлексия вернёт true.
			name: "nil chan",
			v:    (chan int)(nil),
			want: true,
		},
		{
			// nil-слайс: выделение не производилось, IsNil вернёт true.
			name: "nil slice",
			v:    ([]int)(nil),
			want: true,
		},
		{
			// nil-функция: тип является ссылочным, IsNil вернёт true.
			name: "nil func",
			v:    (func())(nil),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNilInterface(tt.v); got != tt.want {
				t.Errorf("isNilInterface(%#v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}
