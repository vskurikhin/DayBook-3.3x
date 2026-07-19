// Package api содержит типы данных REST API сервиса KV.
package api

// Определяет структуры данных, используемые REST API для взаимодействия
// между kvservice и клиентами. Эти структуры сериализуются в JSON и
// передаются в теле HTTP-запросов и HTTP-ответов.
//
// Вместо стандартных HTTP-кодов состояния в каждом ответе используется
// собственный тип ResponseStatus, поскольку такие состояния, как
// «не лидер» или «не удалось зафиксировать команду», не имеют
// подходящих аналогов среди стандартных HTTP-кодов.
//
// Каждый тип запроса содержит поля, обеспечивающие уникальную
// идентификацию запроса.

type PutRequest struct {
	Key   string
	Value string
}

type Response interface {
	Status() ResponseStatus
}

type PutResponse struct {
	RespStatus ResponseStatus
	KeyFound   bool
	PrevValue  string
}

func (pr *PutResponse) Status() ResponseStatus {
	return pr.RespStatus
}

type GetRequest struct {
	Key string
}

type GetResponse struct {
	RespStatus ResponseStatus
	KeyFound   bool
	Value      string
}

func (gr *GetResponse) Status() ResponseStatus {
	return gr.RespStatus
}

type CASRequest struct {
	Key          string
	CompareValue string
	Value        string
}

type CASResponse struct {
	RespStatus ResponseStatus
	KeyFound   bool
	PrevValue  string
}

func (cr *CASResponse) Status() ResponseStatus {
	return cr.RespStatus
}

type ResponseStatus int

const (
	StatusInvalid ResponseStatus = iota
	StatusOK
	StatusNotLeader
	StatusFailedCommit
)

var responseName = map[ResponseStatus]string{
	StatusInvalid:      "invalid",
	StatusOK:           "OK",
	StatusNotLeader:    "NotLeader",
	StatusFailedCommit: "FailedCommit",
}

func (rs ResponseStatus) String() string {
	return responseName[rs]
}
