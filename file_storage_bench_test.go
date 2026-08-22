package raft

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
)

const (
	// benchValueSizeSmall — нижняя граница размера значения: показывает
	// стоимость собственно синхронизации файла с диском.
	benchValueSizeSmall = 64

	// benchValueSizeLog — размер, сопоставимый с журналом работающего узла.
	benchValueSizeLog = 97 * 1024
)

// benchValueSizes — размеры значений, в которых выполняется каждый бенчмарк.
var benchValueSizes = []struct {
	name string
	size int
}{
	{name: "small", size: benchValueSizeSmall},
	{name: "log", size: benchValueSizeLog},
}

// benchValue детерминированно формирует значение размером size байт.
func benchValue(size int, seed int64) []byte {
	rnd := rand.New(rand.NewSource(seed))
	value := make([]byte, size)
	for i := range value {
		value[i] = byte(rnd.Intn(256))
	}
	return value
}

// BenchmarkFileStorageSetSameValue измеряет повторную запись одного ключа
// одним и тем же значением.
func BenchmarkFileStorageSetSameValue(b *testing.B) {
	for _, variant := range benchValueSizes {
		b.Run(variant.name, func(b *testing.B) {
			storage := NewFileStorage(b.TempDir())
			value := benchValue(variant.size, 1)
			// Первая запись ключа выполняется до измерения: она неизбежна
			// (ключа ещё нет на диске) и измеряется отдельным бенчмарком.
			// Измеряется установившийся режим, в котором значение на диске
			// уже совпадает с записываемым.
			storage.Set("log", value)
			before := storage.writeCount()
			b.SetBytes(int64(variant.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				storage.Set("log", value)
			}
			b.StopTimer()
			b.ReportMetric(float64(storage.writeCount()-before)/float64(b.N), "writes/op")
		})
	}
}

// BenchmarkFileStorageSetChangedValue измеряет запись одного ключа значением,
// отличающимся на каждой итерации.
func BenchmarkFileStorageSetChangedValue(b *testing.B) {
	for _, variant := range benchValueSizes {
		b.Run(variant.name, func(b *testing.B) {
			storage := NewFileStorage(b.TempDir())
			value := benchValue(variant.size, 2)
			b.SetBytes(int64(variant.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				next := make([]byte, len(value))
				copy(next, value)
				// Номер итерации в фиксированном префиксе гарантирует, что
				// значение отличается от записанного на предыдущей итерации,
				// поэтому пропуска записи не происходит ни разу.
				binary.LittleEndian.PutUint64(next, uint64(i))
				storage.Set("log", next)
			}
			b.StopTimer()
			b.ReportMetric(float64(storage.writeCount())/float64(b.N), "writes/op")
		})
	}
}

// benchLogEntries формирует журнал, кодированный размер которого близок
// к totalBytes: полезная нагрузка распределена по entries записям.
func benchLogEntries(entries, totalBytes int) []LogEntry {
	payload := totalBytes / entries
	log := make([]LogEntry, 0, entries)
	for i := 0; i < entries; i++ {
		log = append(log, LogEntry{
			Index: i,
			Term:  1,
			Type:  LogCommand,
			Data:  fmt.Sprintf("%0*d", payload, i),
		})
	}
	return log
}
