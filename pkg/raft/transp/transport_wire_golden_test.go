package transp

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// wireProbe — контейнер, через который конкретное значение RPC-типа
// кодируется в интерфейсном поле. gob пишет метку конкретного типа в
// поток только тогда, когда тип хранится в значении интерфейса; прямое
// кодирование значения неинтерфейсного типа метку не записывает.
type wireProbe struct {
	Value any
}

// TestWireLabelsGolden — постоянный эталонный тест меток gob.
//
// Метки десяти RPC-типов закреплены в init() (transport_tcp.go) полным
// путём импорта корневого пакета github.com/vskurikhin/raft и являются
// частью контракта: изменение или удаление метки
// (например, «исправление» на contract.<ИмяТипа>) нарушило бы
// декодирование RPC между узлами разных версий кластера.
// Эталонные метки хранятся в testdata/wire_labels.txt
// (по одной строке на каждый тип).
//
// Тест для каждого типа кодирует значение через интерфейсное поле и
// требует вхождения байт эталонной метки в кодированный поток. При
// несовпадении — fatal с ожидаемой и фактической метками.
func TestWireLabelsGolden(t *testing.T) {
	labels := readWireLabels(t)
	if len(labels) != 10 {
		t.Fatalf("testdata/wire_labels.txt содержит %d строк, want 10", len(labels))
	}

	tests := []struct {
		name  string
		value any
	}{
		{"AppendEntriesArgs", contract.AppendEntriesArgs{}},
		{"AppendEntriesReply", contract.AppendEntriesReply{}},
		{"RequestVoteArgs", contract.RequestVoteArgs{}},
		{"RequestVoteReply", contract.RequestVoteReply{}},
		{"TimeoutNowRequest", contract.TimeoutNowRequest{}},
		{"TimeoutNowResponse", contract.TimeoutNowResponse{}},
		{"RequestPreVoteArgs", contract.RequestPreVoteArgs{}},
		{"RequestPreVoteReply", contract.RequestPreVoteReply{}},
		{"InstallSnapshotRequest", contract.InstallSnapshotRequest{}},
		{"InstallSnapshotResponse", contract.InstallSnapshotResponse{}},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := labels[i]
			wantLabel := "github.com/vskurikhin/raft." + tt.name
			if want != wantLabel {
				t.Fatalf("эталонная метка %q не совпадает с ожидаемой %q", want, wantLabel)
			}

			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(&wireProbe{Value: tt.value}); err != nil {
				t.Fatalf("gob.Encode(%s): %v", tt.name, err)
			}
			if !bytes.Contains(buf.Bytes(), []byte(want)) {
				t.Fatalf(
					"поток для %s не содержит метку %q; фактический поток:\n%x",
					tt.name, want, buf.Bytes(),
				)
			}
		})
	}
}

// readWireLabels читает эталонные метки из testdata/wire_labels.txt,
// отбрасывая пустые строки и ведущие/хвостовые пробелы.
func readWireLabels(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "wire_labels.txt"))
	if err != nil {
		t.Fatalf("не удалось прочитать testdata/wire_labels.txt: %v", err)
	}
	var labels []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		labels = append(labels, line)
	}
	return labels
}
