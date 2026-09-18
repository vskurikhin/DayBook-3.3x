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
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type Response interface {
	Status() ResponseStatus
}

type PutResponse struct {
	RespStatus ResponseStatus `json:"RespStatus"`
	KeyFound   bool           `json:"KeyFound"`
	PrevValue  string         `json:"PrevValue"`
}

var _ Response = (*PutResponse)(nil)

func (pr *PutResponse) Status() ResponseStatus {
	return pr.RespStatus
}

type GetRequest struct {
	Key string `json:"Key"`
}

type GetResponse struct {
	RespStatus ResponseStatus `json:"RespStatus"`
	KeyFound   bool           `json:"KeyFound"`
	Value      string         `json:"Value"`
}

var _ Response = (*GetResponse)(nil)

func (gr *GetResponse) Status() ResponseStatus {
	return gr.RespStatus
}

type CASRequest struct {
	Key          string `json:"Key"`
	CompareValue string `json:"CompareValue"`
	Value        string `json:"Value"`
}

type CASResponse struct {
	RespStatus ResponseStatus `json:"RespStatus"`
	KeyFound   bool           `json:"KeyFound"`
	PrevValue  string         `json:"PrevValue"`
}

var _ Response = (*CASResponse)(nil)

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

// StatusResponse — минимальный ответ, содержащий только статус.
// Используется для VerifyLeader и других операций без данных.
type StatusResponse struct {
	RespStatus ResponseStatus `json:"RespStatus"`
}

var _ Response = (*StatusResponse)(nil)

func (s *StatusResponse) Status() ResponseStatus {
	return s.RespStatus
}

func (rs ResponseStatus) String() string {
	switch rs {
	case StatusInvalid:
		return "invalid"
	case StatusOK:
		return "OK"
	case StatusNotLeader:
		return "NotLeader"
	case StatusFailedCommit:
		return "FailedCommit"
	default:
		return ""
	}
}
