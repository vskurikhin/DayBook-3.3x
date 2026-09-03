package raft

import (
	"testing"
	"time"

	"github.com/vskurikhin/raft/pkg/raft/transp"
)

// newInmemPair — локальная копия для тестов корня: создаёт два
// соединённых InmemTransport с уникальными адресами. Возвращает
// (t1, t2, cleanup). cleanup закрывает оба транспорта. Определение
// перенесено в pkg/raft/transp; здесь — дубль, чтобы тесты корня не
// зависели от приватных хелперов пакета transp.
func newInmemPair(t *testing.T) (*transp.InmemTransport, *transp.InmemTransport, func()) {
	t.Helper()
	t1 := transp.NewInmemTransport("test-0")
	t2 := transp.NewInmemTransport("test-1")
	t1.Connect(1, t2)
	t2.Connect(0, t1)
	return t1, t2, func() {
		t1.Close()
		t2.Close()
	}
}

// newTCPTransportPair — локальная копия для тестов корня: фабрика пары
// соединённых TCPTransport. Возвращает (client, server, cleanup).
// Определение перенесено в pkg/raft/transp; здесь — дубль, чтобы тесты
// корня не зависели от приватных хелперов пакета transp.
func newTCPTransportPair(t *testing.T) (Transport, Transport, func()) {
	t.Helper()
	timeout := time.Second
	server, err := transp.NewTCPTransport("127.0.0.1:0", timeout, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport(server): %v", err)
	}
	client, err := transp.NewTCPTransport("127.0.0.1:0", timeout, 2)
	if err != nil {
		server.Close()
		t.Fatalf("NewTCPTransport(client): %v", err)
	}
	client.Connect(1, string(server.LocalAddr()))
	server.Connect(0, string(client.LocalAddr()))
	return client, server, func() {
		client.Close()
		server.Close()
	}
}
