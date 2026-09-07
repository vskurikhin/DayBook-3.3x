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
const DebugClient = 0

const (
	// defaultRequestTimeout — таймаут на один HTTP-запрос.
	// При недоступности лидера клиент переключается на другой адрес.
	defaultRequestTimeout = 500 * time.Millisecond
)

type KVClient struct {
	addrs []string

	// assumedLeader — индекс (в addrs) сервиса, который в данный момент
	// предполагается лидером кластера. По умолчанию инициализируется нулём,
	// что не влияет на общность алгоритма. Один экземпляр клиента разделяется
	// несколькими горутинами, поэтому индекс читается и меняется атомарно.
	assumedLeader atomic.Int64

	// requestTimeout — таймаут одного HTTP-запроса этого экземпляра клиента.
	requestTimeout time.Duration

	// clientID — уникальный идентификатор клиента. Управляется внутри этого
	// файла путём увеличения глобального счётчика clientCount.
	clientID int32
}

// New создаёт новый экземпляр KVClient. serviceAddrs — список адресов
// (каждый в формате "host:port") сервисов кластера KVService, с которыми
// будет взаимодействовать клиент. Таймаут одного запроса —
// defaultRequestTimeout.
func New(serviceAddrs []string) *KVClient {
	return NewWithTimeout(serviceAddrs, defaultRequestTimeout)
}

// NewWithTimeout создаёт экземпляр KVClient с заданным таймаутом одного
// HTTP-запроса. Нужен измерительным сценариям, где таймаут по умолчанию
// обрезает хвост распределения времени ответа.
func NewWithTimeout(serviceAddrs []string, timeout time.Duration) *KVClient {
	return &KVClient{
		addrs:          serviceAddrs,
		requestTimeout: timeout,
		clientID:       clientCount.Add(1),
	}
}

// leader возвращает индекс адреса, который клиент сейчас считает лидером.
func (c *KVClient) leader() int {
	return int(c.assumedLeader.Load())
}

// nextLeader переводит клиента на следующий адрес списка. При одновременном
// вызове из нескольких горутин один адрес может быть пропущен — это свойство
// исходного алгоритма ротации сохраняется.
func (c *KVClient) nextLeader() {
	c.assumedLeader.Store(int64((c.leader() + 1) % len(c.addrs)))
}

// clientCount используется для назначения уникальных идентификаторов
// различным клиентам.
var clientCount atomic.Int32

// VerifyLeader проверяет, является ли assumedLeader действующим лидером,
// используя ReadIndex-запрос (Raft §8). Возвращает leaderID или ошибку.
// Если текущий assumedLeader не лидер, перебирает остальные адреса.
// Позволяет клиенту быстро обнаружить смену лидера без лишних KV-запросов.
func (c *KVClient) VerifyLeader(ctx context.Context) (int, error) {
	for {
		leader := c.leader()
		path := fmt.Sprintf("http://%s/verifyleader/", c.addrs[leader])

		reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
		var resp api.Response
		err := sendJSONRequest(reqCtx, path, nil, &resp)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return -1, err
			}
			c.nextLeader()
			continue
		}

		switch resp.Status() {
		case api.StatusOK:
			return leader, nil
		case api.StatusNotLeader:
			c.nextLeader()
			continue
		default:
			c.nextLeader()
			continue
		}
	}
}

// Put сохраняет пару key=value в хранилище.
// Возвращает ошибку либо (prevValue, keyFound, nil), где keyFound показывает,
// существовал ли ключ в хранилище до выполнения команды, а prevValue содержит
// его предыдущее значение, если ключ был найден.
func (c *KVClient) Put(ctx context.Context, key, value string) (string, bool, error) {
	putReq := api.PutRequest{
		Key:   key,
		Value: value,
	}
	var putResp api.PutResponse
	err := c.send(ctx, "put", putReq, &putResp)
	return putResp.PrevValue, putResp.KeyFound, err
}

// Get получает значение по ключу.
// Возвращает ошибку либо (value, found, nil), где found показывает,
// существует ли указанный ключ в хранилище.
func (c *KVClient) Get(ctx context.Context, key string) (string, bool, error) {
	getReq := api.GetRequest{
		Key: key,
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
func (c *KVClient) CAS(ctx context.Context, key, compare, value string) (string, bool, error) {
	casReq := api.CASRequest{
		Key:          key,
		CompareValue: compare,
		Value:        value,
	}
	var casResp api.CASResponse
	err := c.send(ctx, "cas", casReq, &casResp)
	return casResp.PrevValue, casResp.KeyFound, err
}

func (c *KVClient) send(ctx context.Context, route string, req any, resp api.Response) error {
	for {
		reqCtx, reqCtxCancel := context.WithTimeout(ctx, c.requestTimeout)
		path := fmt.Sprintf("http://%s/%s/", c.addrs[c.leader()], route)

		c.clientLogf("sending %#v to %v", req, path)
		if err := sendJSONRequest(reqCtx, path, req, resp); err != nil {
			reqCtxCancel()
			if ctx.Err() != nil {
				return err
			}
			c.clientLogf("request failed: %v; switching to next address", err)
			c.nextLeader()
			continue
		}
		reqCtxCancel()
		c.clientLogf("received response %#v", resp)

		switch resp.Status() {
		case api.StatusNotLeader:
			c.clientLogf("not leader: will try next address")
			c.nextLeader()
			continue
		case api.StatusOK:
			return nil
		case api.StatusFailedCommit:
			return fmt.Errorf("commit failed; please retry")
		default:
			panic("unreachable")
		}
	}
}

// clientLogf выводит отладочное сообщение, если DebugClient > 0.
func (c *KVClient) clientLogf(format string, args ...any) {
	if DebugClient > 0 {
		clientName := fmt.Sprintf("[client%03d]", c.clientID)
		format = clientName + " " + format
		log.Printf(format, args...)
	}
}

func sendJSONRequest(ctx context.Context, path string, reqData, respData any) error {
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
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(respData); err != nil {
		return fmt.Errorf("JSON-decoding response data: %w", err)
	}
	return nil
}
