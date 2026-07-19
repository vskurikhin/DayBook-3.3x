package kvservice

// Command — конкретный тип команды, который KVService записывает в журнал
// Raft для управления своей машиной состояний. После применения команды к
// машине состояний эта же структура используется для передачи результата
// выполнения команды.
//
// Поддерживаются следующие типы команд:
//
// CommandGet — получение значения по ключу.
//
//   - Key — ключ, значение которого требуется получить; поле Value игнорируется.
//   - CompareValue игнорируется.
//   - ResultFound равно true, если Key найден в хранилище.
//   - ResultValue содержит значение ключа, если он найден.
//
// CommandPut — запись значения по ключу.
//
//   - Key и Value — пара, которую необходимо сохранить
//     (Store[Key] = Value).
//   - CompareValue игнорируется.
//   - ResultFound равно true, если Key уже существовал в хранилище.
//   - ResultValue содержит прежнее значение Key, если ключ существовал.
//
// CommandCAS — атомарная операция compare-and-swap:
//
//		if Store[Key] == CompareValue {
//		    Store[Key] = Value
//		} else {
//		    // ничего не делать
//		}
//
//	  * Key — ключ, над которым выполняется операция.
//	  * CompareValue — значение, с которым сравнивается текущее значение ключа.
//	  * Value — новое значение, записываемое при успешном сравнении.
//	  * ResultFound равно true, если Key существовал в хранилище.
//	  * ResultValue содержит прежнее значение Key, если ключ существовал.
type Command struct {
	Kind CommandKind

	Key, Value string

	CompareValue string

	ResultValue string
	ResultFound bool

	// Id — идентификатор Raft-сервиса, отправившего данную команду.
	Id int
}

type CommandKind int

const (
	CommandInvalid CommandKind = iota
	CommandGet
	CommandPut
	CommandCAS
)

var commandName = map[CommandKind]string{
	CommandInvalid: "invalid",
	CommandGet:     "get",
	CommandPut:     "put",
	CommandCAS:     "cas",
}

func (ck CommandKind) String() string {
	return commandName[ck]
}
