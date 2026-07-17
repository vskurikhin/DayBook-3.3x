// Package kvclient — библиотека клиента KV.
// Go-приложениям, взаимодействующим с KV-сервисом, следует использовать
// этот клиент вместо непосредственной отправки REST-запросов.
package kvclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/vskurikhin/raft/pkg/api"
)

// DebugClient включает вывод отладочной информации.
const DebugClient = 1

type KVClient struct {
	addrs []string

	// assumedLeader — индекс (в addrs) сервиса, который в данный момент
	// предполагается лидером кластера. По умолчанию инициализируется нулём,
	// что не влияет на общность алгоритма.
	assumedLeader int

	// clientID — уникальный идентификатор клиента. Управляется внутри этого
	// файла путём увеличения глобального счётчика clientCount.
	clientID int64

	// requestID — уникальный идентификатор запроса, отправленного данным
	// клиентом. Каждый клиент самостоятельно ведёт свой requestID и
	// монотонно увеличивает его атомарно при каждом новом запросе.
	requestID atomic.Int64
}

// New создаёт новый экземпляр KVClient. serviceAddrs — список адресов
// (каждый в формате "host:port") сервисов кластера KVService, с которыми
// будет взаимодействовать клиент.
func New(serviceAddrs []string) *KVClient {
	return &KVClient{
		addrs:         serviceAddrs,
		assumedLeader: 0,
		clientID:      clientCount.Add(1),
	}
}

// clientCount используется для назначения уникальных идентификаторов
// различным клиентам.
var clientCount atomic.Int64

// Put сохраняет пару key=value в хранилище.
// Возвращает ошибку либо (prevValue, keyFound, nil), где keyFound показывает,
// существовал ли ключ в хранилище до выполнения команды, а prevValue содержит
// его предыдущее значение, если ключ был найден.
func (c *KVClient) Put(ctx context.Context, key string, value string) (string, bool, error) {
	// Каждый запрос получает уникальный идентификатор, состоящий из ID клиента
	// и ID запроса внутри этого клиента. Структура с этим идентификатором
	// передаётся в s.send, который может повторять отправку запроса до успешного
	// завершения. Уникальный идентификатор позволяет сервису обнаруживать и
	// устранять дублирование запросов, которые могут поступить несколько раз
	// из-за проблем сети и повторных попыток клиента.
	putReq := api.PutRequest{
		Key:       key,
		Value:     value,
		ClientID:  c.clientID,
		RequestID: c.requestID.Add(1),
	}
	var putResp api.PutResponse
	err := c.send(ctx, "put", putReq, &putResp)
	return putResp.PrevValue, putResp.KeyFound, err
}

// Append добавляет value к значению по ключу.
// Возвращает ошибку либо (prevValue, keyFound, nil), где keyFound показывает,
// существовал ли ключ в хранилище до выполнения команды, а prevValue содержит
// его предыдущее значение, если ключ был найден.
func (c *KVClient) Append(ctx context.Context, key string, value string) (string, bool, error) {
	appendReq := api.AppendRequest{
		Key:       key,
		Value:     value,
		ClientID:  c.clientID,
		RequestID: c.requestID.Add(1),
	}
	var appendResp api.AppendResponse
	err := c.send(ctx, "append", appendReq, &appendResp)
	return appendResp.PrevValue, appendResp.KeyFound, err
}

// Get получает значение по ключу.
// Возвращает ошибку либо (value, found, nil), где found показывает,
// существует ли указанный ключ в хранилище.
func (c *KVClient) Get(ctx context.Context, key string) (string, bool, error) {
	getReq := api.GetRequest{
		Key:       key,
		ClientID:  c.clientID,
		RequestID: c.requestID.Add(1),
	}
	var getResp api.GetResponse
	err := c.send(ctx, "get", getReq, &getResp)
	return getResp.Value, getResp.KeyFound, err
}

// CAS операция: если текущее значение ключа совпадает с compare,
// записывается новое значение value.
// Возвращает ошибку либо (prevValue, keyFound, nil), где keyFound показывает,
// существовал ли ключ до выполнения команды, а prevValue содержит его
// предыдущее значение, если ключ был найден.
func (c *KVClient) CAS(ctx context.Context, key string, compare string, value string) (string, bool, error) {
	casReq := api.CASRequest{
		Key:          key,
		CompareValue: compare,
		Value:        value,
		ClientID:     c.clientID,
		RequestID:    c.requestID.Add(1),
	}
	var casResp api.CASResponse
	err := c.send(ctx, "cas", casReq, &casResp)
	return casResp.PrevValue, casResp.KeyFound, err
}

func (c *KVClient) send(ctx context.Context, route string, req any, resp api.Response) error {
	// Этот цикл перебирает список адресов сервисов до тех пор, пока не получит
	// ответ, подтверждающий, что найден лидер кластера. Начинает поиск с
	// c.assumedLeader.
FindLeader:
	for {
		// Здесь используется двухуровневое дерево контекстов: пользовательский
		// контекст ctx и созданный нами дочерний контекст с тайм-аутом для
		// каждого отдельного запроса к сервису. Если тайм-аут дочернего
		// контекста истекает, выполняется попытка обращения к следующему
		// сервису. При этом необходимо постоянно отслеживать пользовательский
		// контекст: если он будет отменён (по тайм-ауту, явной отмене и т.п.),
		// выполнение немедленно прекращается.
		retryCtx, retryCtxCancel := context.WithTimeout(ctx, 50*time.Millisecond)
		path := fmt.Sprintf("http://%s/%s/", c.addrs[c.assumedLeader], route)

		c.clientlog("sending %#v to %v", req, path)
		if err := sendJSONRequest(retryCtx, path, req, resp); err != nil {
			// Поскольку контексты вложены друг в друга, порядок проверок имеет
			// значение. Сначала необходимо проверить родительский контекст —
			// если он завершён, нужно немедленно вернуть управление.
			if contextDone(ctx) {
				c.clientlog("parent context done; bailing out")
				retryCtxCancel()
				return err
			} else if contextDeadlineExceeded(retryCtx) {
				// Если родительский контекст ещё активен, а истёк только
				// дочерний контекст повторной попытки, необходимо обратиться
				// к следующему сервису.
				c.clientlog("timed out: will try next address")
				c.assumedLeader = (c.assumedLeader + 1) % len(c.addrs)
				retryCtxCancel()
				continue FindLeader
			}
			retryCtxCancel()
			return err
		}
		c.clientlog("received response %#v", resp)

		// Для этого этапа контекст и тайм-аут больше не учитываются —
		// ответ от сервиса уже успешно получен.
		switch resp.Status() {
		case api.StatusNotLeader:
			c.clientlog("not leader: will try next address")
			c.assumedLeader = (c.assumedLeader + 1) % len(c.addrs)
			retryCtxCancel()
			continue FindLeader
		case api.StatusOK:
			retryCtxCancel()
			return nil
		case api.StatusFailedCommit:
			retryCtxCancel()
			return fmt.Errorf("commit failed; please retry")
		case api.StatusDuplicateRequest:
			retryCtxCancel()
			return fmt.Errorf("this request was already completed")
		default:
			panic("unreachable")
		}
	}
}

// clientlog выводит отладочное сообщение, если DebugClient > 0.
func (c *KVClient) clientlog(format string, args ...any) {
	if DebugClient > 0 {
		clientName := fmt.Sprintf("[client%03d]", c.clientID)
		format = clientName + " " + format
		log.Printf(format, args...)
	}
}

func sendJSONRequest(ctx context.Context, path string, reqData any, respData any) error {
	body := new(bytes.Buffer)
	enc := json.NewEncoder(body)
	if err := enc.Encode(reqData); err != nil {
		return fmt.Errorf("JSON-encoding request data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, body)
	if err != nil {
		return fmt.Errorf("creating HTTP request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(respData); err != nil {
		return fmt.Errorf("JSON-decoding response data: %w", err)
	}
	return nil
}

// contextDone проверяет, завершён ли контекст ctx по любой причине.
// Функция не блокируется.
func contextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
	}
	return false
}

// contextDeadlineExceeded проверяет, завершился ли контекст ctx из-за
// истечения времени ожидания (deadline). Функция не блокируется.
func contextDeadlineExceeded(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return true
		}
	default:
	}
	return false
}
