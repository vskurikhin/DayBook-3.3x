package raft

import (
	"errors"
	"log"
	"os"
	"sync/atomic"
)

// _traceCM — порог детализации отладочных сообщений консенсус-модуля
// (traceLogf, traceLogLocked). Сообщение с уровнем level выводится,
// если _traceCM > level. Значение задаётся полем TraceConfig.Level
// единственного успешного вызова SetTrace, по умолчанию 0.
// Изменяется только до старта горутин.
var _traceCM = 0

// _traceLogger — логгер отладочных сообщений консенсус-модуля (traceLogf,
// traceLogfLocked). По умолчанию выводит в стандартный логгер (stderr).
// После единственной успешной конфигурации SetTrace — read-only.
var _traceLogger = log.Default()

// traceEnabled сообщает, будет ли сообщение уровня level напечатано
// трассировкой. Единственный источник истины для порога _traceCM.
func traceEnabled(level int) bool { return level < _traceCM }

// Уровни сообщений трассировки консенсус-модуля (порог _traceCM:
// сообщение уровня l печатается, если l < _traceCM). Уровни — ступени
// подробности: увеличение порога добавляет новый слой сообщений,
// не отключая предыдущие. Значения и состав ступеней — операционный
// контракт вывода, они не меняются.
const (
	// _traceLevelKeyEvents — ключевые события: смена роли и терма,
	// отправка и приём RequestVote, TimeoutNow, отказы отправки
	// и создания снимка. Печатаются при пороге от 1.
	_traceLevelKeyEvents = 0

	// _traceLevelLoops — течение циклов выборов и лидера: таймер
	// выборов, входы и выходы из циклов, шаг вниз лидера.
	// Печатаются при пороге от 2.
	_traceLevelLoops = 1

	// _traceLevelProgress — результативные события: победа
	// в выборах, продвижение индекса фиксации, длительность
	// персиста. Печатаются при пороге от 3.
	_traceLevelProgress = 2

	// _traceLevelPreVote — предварительное голосование: отправка,
	// отказы и тайм-ауты pre-vote, причины отказов в голосах;
	// детали отправки снимка ведомому. Печатаются при пороге от 5.
	_traceLevelPreVote = 4

	// _traceLevelReplication — репликация: обработка RPC (handleRPC,
	// AppendEntries и ответы), отправка AppendEntries, пульс.
	// Печатаются при пороге от 9.
	_traceLevelReplication = 8

	// _traceLevelLogDump — полный дамп журнала после изменения.
	// Печатается при пороге от 17.
	_traceLevelLogDump = 16
)

// _traceConfigured — сторожевой флаг строгого set-once: единственная
// успешная конфигурация трассировки на процесс уже выполнена.
// Устанавливается только успешным вызовом SetTrace
// (вызов, завершившийся ошибкой I/O, окно не расходует).
var _traceConfigured atomic.Bool

// _traceCMCreated — сторожевой флаг: в процессе уже создавался
// ConsensusModule. Устанавливается конструктором.
var _traceCMCreated atomic.Bool

// TraceConfig — параметры трассировки консенсус-модуля, передаваемые
// SetTrace. Тип является простым носителем значений: у него нет методов
// и он не подразумевает расширения поведением.
type TraceConfig struct {
	// Level — порог детализации: сообщение уровня l печатается, если
	// l < Level. Level == 0 означает, что трассировка выключена;
	// нулевое значение НЕ трактуется как «уровень по умолчанию 1».
	Level int

	// LogFile — путь к файлу журнала трассировки. Файл открывается в
	// режиме добавления и создаётся при необходимости. Пустая строка
	// означает вывод в стандартный логгер (stderr).
	LogFile string
}

// SetTrace конфигурирует трассировку консенсус-модуля (traceLogf,
// traceLogfLocked): порог детализации cfg.Level и назначение вывода
// cfg.LogFile. Пустой cfg.LogFile — вывод в стандартный логгер; иначе
// файл открывается в режиме добавления и создаётся при необходимости.
// Значение cfg.Level == 0 выключает трассировку и не подменяется
// значением по умолчанию.
//
// Контракт (строгий set-once-before-first-CM): функция успешна ровно
// один раз на процесс, до создания первого ConsensusModule (и, тем
// самым, до входа CM в Candidate/Candidate-Modifier). Первый успешный
// вызов выигрывает и замораживает конфигурацию — и логгер, и порог;
// состояние трассировки после этого read-only. Любой повторный вызов —
// детерминированная ошибка контракта, в том числе до создания CM;
// вызов после создания CM — та же ошибка. Вызов, завершившийся ошибкой
// I/O, окно конфигурации не расходует и не изменяет состояние
// трассировки.
//
// SetTrace — единственная точка конфигурации трассировки пакета.
func SetTrace(cfg TraceConfig) error {
	if _traceConfigured.Load() || _traceCMCreated.Load() {
		return errors.New(
			"raft: trace configuration must be set exactly once, " +
				"before the first ConsensusModule is created; repeated " +
				"calls are forbidden",
		)
	}
	if cfg.LogFile == "" {
		_traceCM = cfg.Level
		_traceLogger = log.Default()
		_traceConfigured.Store(true)
		return nil
	}
	// Порог присваивается только после успешного открытия файла:
	// вызов, завершившийся ошибкой I/O, окно конфигурации не расходует
	// и обязан оставить состояние трассировки прежним.
	f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_traceCM = cfg.Level
	_traceLogger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	_traceConfigured.Store(true)
	return nil
}
