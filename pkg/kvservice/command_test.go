package kvservice

import "testing"

// TestCommandKindString фиксирует значения String() всех видов команд
// и возврат пустой строки для неизвестного значения — контракт
// fmt-печати (в том числе трассировки неизвестной команды),
// сохраняемый при любом изменении реализации.
func TestCommandKindString(t *testing.T) {
	tests := []struct {
		name string
		ck   CommandKind
		want string
	}{
		{"CommandInvalid", CommandInvalid, "invalid"},
		{"CommandGet", CommandGet, "get"},
		{"CommandPut", CommandPut, "put"},
		{"CommandCAS", CommandCAS, "cas"},
		{"CommandDelete", CommandDelete, "delete"},
		{"unknown", CommandKind(42), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ck.String(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCommandKindValues закрепляет числовые значения CommandKind: поле Kind
// сериализуется в журнал Raft числом (gob.Register(Command{})), поэтому
// переупорядочивание или вставка в середину блока const изменит смысл
// уже записанных команд. Значения Invalid/Get/Put/CAS/Delete = 0/1/2/3/4
// фиксируются машинно; CommandDelete — последним элементом блока.
func TestCommandKindValues(t *testing.T) {
	if !(CommandInvalid == 0 && CommandGet == 1 && CommandPut == 2 &&
		CommandCAS == 3 && CommandDelete == 4) {
		t.Fatalf(
			"CommandKind values changed: Invalid=%d Get=%d Put=%d CAS=%d Delete=%d",
			CommandInvalid, CommandGet, CommandPut, CommandCAS, CommandDelete,
		)
	}
}
