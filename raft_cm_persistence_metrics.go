package raft

import (
	"encoding/json"
	"time"
)

const (
	// _statsPersistLineLimit — предельный размер тела строки PersistV1
	// в байтах. Лимит относится только к этой строке: старую строку
	// с парами соседей он не ограничивает.
	_statsPersistLineLimit = 16 * 1024

	// _statsGroupUnavailable — маркер ещё не реализованной группы метрик.
	// Недоступность обозначается явно и не подменяется нулевыми значениями:
	// ноль наблюдаемого счётчика и отсутствие возможности — разные факты.
	_statsGroupUnavailable = "unavailable"

	// _statsPersistOverflow — короткий маркер превышения предела строки;
	// заменяет текст ошибки, если тот не помещается в документ.
	_statsPersistOverflow = "persistV1 line exceeds 16 KiB limit"
)

// statsPersistV1 — документ третьей строки периодического отчёта: машиночитаемая
// сводка выпуска. Накопительные значения позволяют восстановить дельту через
// пропущенную строку при неизменном Instance и известных границах; смена
// Instance, повторный Seq и уменьшение сумм считаются дефектами ряда.
type statsPersistV1 struct {
	// Schema — версия схемы документа; первая версия — 1.
	Schema int `json:"Schema"`
	// Instance — идентификатор экземпляра CM: различает ряды одного узла
	// после перезапуска и разные CM в процессе.
	Instance uint64 `json:"Instance"`
	// Seq — номер попытки выпуска всего отчёта; при выключенном выводе
	// не растёт.
	Seq uint64 `json:"Seq"`
	// Utc — момент снятия снимка в UTC.
	Utc string `json:"Utc"`
	// AgeNs — монотонный возраст CM от создания в наносекундах.
	AgeNs int64 `json:"AgeNs"`
	// Role — роль узла на момент снятия.
	Role string `json:"Role"`
	// Term — текущий терм узла.
	Term int `json:"Term"`
	// CmSnapshotNs — момент снятия снимка CM в наносекундах эпохи Unix.
	CmSnapshotNs int64 `json:"CmSnapshotNs"`
	// StorageSnapshotNs — момент отдельного диагностического снимка
	// хранилища; null, пока возможность не реализована.
	StorageSnapshotNs *int64 `json:"StorageSnapshotNs"`
	// Persistence — доступность группы счётчиков сохранений.
	Persistence string `json:"Persistence"`
	// Dirty — доступность группы грязных периодов журнала.
	Dirty string `json:"Dirty"`
	// OutputError — липкая ошибка общего вывода отчёта; пустая строка
	// означает отсутствие ошибок.
	OutputError string `json:"OutputError"`
}

// persistReport формирует третью строку отчёта — JSON-документ PersistV1:
// версия схемы, экземпляр, номер выпуска, UTC и монотонный возраст CM,
// роль/терм, моменты снимков CM/Storage, доступность групп метрик и липкая
// ошибка общего вывода. Группы последующих задач этапа обозначены
// unavailable: до их реализации значения не публикуются.
//
// maxLen — предел размера тела документа, оставляющий место обязательному
// префиксу строки. При превышении публикуется тот же формат с коротким
// маркером вместо текста ошибки: обрезать JSON посередине запрещено —
// принимающая сторона обязана всегда получать корректный JSON.
func (s *statsSnapshot) persistReport(seq uint64, outputErr error, maxLen int) string {
	doc := s.persistDocument(seq, "")
	if outputErr != nil {
		doc.OutputError = outputErr.Error()
	}
	data, err := json.Marshal(doc)
	if err == nil && len(data) <= maxLen {
		return string(data)
	}
	doc.OutputError = _statsPersistOverflow
	data, err = json.Marshal(doc)
	if err != nil || len(data) > maxLen {
		return `{"Schema":1,"OutputError":"persistV1 line exceeds 16 KiB limit"}`
	}
	return string(data)
}

// persistDocument собирает документ PersistV1 из снимка. Все поля — скаляры
// и строки; ссылок на изменяемые данные CM документ не содержит. Момент
// снимка Storage пока недоступен и публикуется как null.
func (s *statsSnapshot) persistDocument(seq uint64, outputError string) statsPersistV1 {
	return statsPersistV1{
		Schema:            1,
		Instance:          s.instance,
		Seq:               seq,
		Utc:               s.at.UTC().Format(time.RFC3339Nano),
		AgeNs:             s.age.Nanoseconds(),
		Role:              s.role.String(),
		Term:              s.term,
		CmSnapshotNs:      s.at.UnixNano(),
		StorageSnapshotNs: nil,
		Persistence:       _statsGroupUnavailable,
		Dirty:             _statsGroupUnavailable,
		OutputError:       outputError,
	}
}
