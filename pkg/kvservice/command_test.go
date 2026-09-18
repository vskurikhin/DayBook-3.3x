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
