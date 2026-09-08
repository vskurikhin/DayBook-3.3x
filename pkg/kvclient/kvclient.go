package kvclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/vskurikhin/raft/pkg/api"
)

// DebugClient включает вывод отладочной информации.
const DebugClient = 0

const (
	// _defaultRequestTimeout — таймаут на один HTTP-запрос.
	// При недоступности лидера клиент переключается на другой адрес.
	_defaultRequestTimeout = 500 * time.Millisecond
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
	// файла путём увеличения глобального счётчика _clientCount.
	clientID int32
}

var (
	// errMethodNotAllowed — сервер ответил 405 на GET-запрос слабого
	// чтения или проверки лидерства: метод маршрута не совпадает
	// с версией клиента и сервера.
	errMethodNotAllowed = errors.New("server returned 405 Method Not Allowed")

	// errRouteMismatch — сервер ответил 404: маршрут не совпал.
	// Детерминирован для всех узлов — ротация бессмысленна.
	errRouteMismatch = errors.New("server returned 404 Not Found")
)

// New создаёт новый экземпляр KVClient. serviceAddrs — список адресов
// (каждый в формате "host:port") сервисов кластера KVService, с которыми
// будет взаимодействовать клиент. Таймаут одного запроса —
// _defaultRequestTimeout.
func New(serviceAddrs []string) *KVClient {
	return NewWithTimeout(serviceAddrs, _defaultRequestTimeout)
}

// NewWithTimeout создаёт экземпляр KVClient с заданным таймаутом одного
// HTTP-запроса. Нужен измерительным сценариям, где таймаут по умолчанию
// обрезает хвост распределения времени ответа.
func NewWithTimeout(serviceAddrs []string, timeout time.Duration) *KVClient {
	return &KVClient{
		addrs:          serviceAddrs,
		requestTimeout: timeout,
		clientID:       _clientCount.Add(1),
	}
}

// _clientCount используется для назначения уникальных идентификаторов
// различным клиентам.
var _clientCount atomic.Int32

// VerifyLeader проверяет, является ли assumedLeader действующим лидером,
// используя ReadIndex-запрос (Raft §8). Запрос выполняется методом GET
// без тела; ответ — StatusResponse. Возвращает leaderID или ошибку.
// Если текущий assumedLeader не лидер, перебирает остальные адреса.
// Позволяет клиенту быстро обнаружить смену лидера без лишних KV-запросов.
func (c *KVClient) VerifyLeader(ctx context.Context) (int, error) {
	for {
		leader := c.leader()
		path := fmt.Sprintf("http://%s/verifyleader/", c.addrs[leader])

		reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
		var sr api.StatusResponse
		err := sendJSONGetRequest(reqCtx, path, &sr)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return -1, err
			}
			// Метод маршрута одинаков для всех узлов кластера —
			// повтор по другому адресу бессмыслен.
			if errors.Is(err, errMethodNotAllowed) || errors.Is(err, errRouteMismatch) {
				return -1, err
			}
			c.nextLeader()
			continue
		}

		switch sr.Status() {
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

// ConsensusGet выполняет сильное чтение по ключу через консенсус: команда
// CommandGet проходит через журнал (как Put), результат возвращается
// future'ом по применению. Возвращает ошибку либо (value, found, nil),
// где found показывает, существует ли указанный ключ в хранилище.
func (c *KVClient) ConsensusGet(ctx context.Context, key string) (string, bool, error) {
	getReq := api.GetRequest{
		Key: key,
	}
	var getResp api.GetResponse
	err := c.send(ctx, "get", getReq, &getResp)
	return getResp.Value, getResp.KeyFound, err
}

// WeakGet метод реализует операцию слабого чтения значения по заданному ключу.
// Операция не приводит к записи в журнал консенсуса Raft и выполняется только
// после подтверждения лидерства с использованием механизма ReadIndex (Raft §8).
// Возвращаемое значение представляет собой либо ошибку, либо тройку (value, found, nil),
// где флаг found отражает факт наличия ключа в хранилище.
// Ключ извлекается из суффикса пути HTTP-запроса GET /weak-get/.
// Для безопасного использования ключа в URL применяется кодирование через url.PathEscape
// с дополнительным экранированием ключей, содержащих точку.
func (c *KVClient) WeakGet(ctx context.Context, key string) (string, bool, error) {
	var getResp api.GetResponse
	err := c.sendGet(ctx, "weak-get", key, &getResp)
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

// Delete удаляет ключ, пропуская команду CommandDelete через
// консенсус (как Put). Возвращает прежнее значение ключа и признак
// его существования до удаления.
func (c *KVClient) Delete(ctx context.Context, key string) (string, bool, error) {
	delReq := api.DeleteRequest{
		Key: key,
	}
	var delResp api.DeleteResponse
	err := c.send(ctx, "delete", delReq, &delResp)
	return delResp.PrevValue, delResp.KeyFound, err
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
			return errors.New("commit failed; please retry")
		default:
			panic("unreachable")
		}
	}
}

// sendGet выполняет GET-запрос слабого чтения с ротацией адресов —
// аналогично send. Отличия: ключ в path-сегменте (URL-кодирование),
// запрос без тела и досрочный возврат при 405 — метод маршрута
// одинаков для всех узлов, повтор по другому адресу бессмыслен.
func (c *KVClient) sendGet(ctx context.Context, route, key string, resp api.Response) error {
	for {
		reqCtx, reqCtxCancel := context.WithTimeout(ctx, c.requestTimeout)
		path := fmt.Sprintf("http://%s/%s/%s", c.addrs[c.leader()], route, pathKey(key))

		c.clientLogf("sending GET key=%v to %v", key, path)
		if err := sendJSONGetRequest(reqCtx, path, resp); err != nil {
			reqCtxCancel()
			if ctx.Err() != nil {
				return err
			}
			if errors.Is(err, errMethodNotAllowed) || errors.Is(err, errRouteMismatch) {
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
			return errors.New("commit failed; please retry")
		default:
			panic("unreachable")
		}
	}
}

// pathKey кодирует ключ для path /weak-get/: PathEscape
// не кодирует точку, а сырой dot-сегмент ("."/"..") переписывается
// маршрутизатором (307) — полные dot-ключи экранируются вручную.
func pathKey(key string) string {
	switch key {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	default:
		return url.PathEscape(key)
	}
}

// sendJSONGetRequest выполняет GET-запрос без тела и декодирует
// JSON-ответ. Ответ с состоянием, отличным от 200, — ошибка; 405 —
// errMethodNotAllowed; 404 — errRouteMismatch.
func sendJSONGetRequest(ctx context.Context, path string, respData any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
	if err != nil {
		return fmt.Errorf("creating HTTP request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusMethodNotAllowed {
			return errMethodNotAllowed
		}
		if resp.StatusCode == http.StatusNotFound {
			return errRouteMismatch
		}
		return fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(respData); err != nil {
		return fmt.Errorf("JSON-decoding response data: %w", err)
	}
	return nil
}

// clientLogf выводит отладочное сообщение, если DebugClient > 0.
func (c *KVClient) clientLogf(format string, args ...any) {
	if DebugClient > 0 {
		clientName := fmt.Sprintf("[client%03d] ", c.clientID)
		log.Printf(clientName+format, args...)
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
