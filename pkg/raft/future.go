// Package raft — реализация протокола консенсуса Raft (v3).
package raft

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrNotLeader      = errors.New("raft: not leader")
	ErrLeadershipLost = errors.New("raft: leadership lost while committing")
	ErrRaftShutdown   = errors.New("raft: raft is shutdown")
	ErrEnqueueTimeout = errors.New("raft: timeout enqueuing operation")
)

// Future представляет асинхронную операцию Raft.
type Future interface {
	Error() error
}

// IndexFuture — Future, возвращающая индекс записи в журнале.
type IndexFuture interface {
	Future
	Index() int
}

// ApplyFuture — Future для Apply-операции.
type ApplyFuture interface {
	IndexFuture
	Response() any
}

// deferError — базовая реализация Future с каналом завершения.
type deferError struct {
	err        error
	errCh      chan error
	once       sync.Once
	responded  bool
	shutdownCh chan struct{}
}

func (d *deferError) init(shutdownCh chan struct{}) {
	d.errCh = make(chan error, 1)
	d.shutdownCh = shutdownCh
}

func (d *deferError) Error() error {
	d.once.Do(func() {
		select {
		case d.err = <-d.errCh:
		case <-d.shutdownCh:
			d.err = ErrRaftShutdown
		}
	})
	return d.err
}

func (d *deferError) respond(err error) {
	if d.errCh == nil || d.responded {
		return
	}
	d.errCh <- err
	close(d.errCh)
	d.responded = true
}

// logFuture — future для одной записи журнала.
type logFuture struct {
	deferError
	log      LogEntry
	response any
	dispatch time.Time
}

func (l *logFuture) Index() int {
	return l.log.Index
}

func (l *logFuture) Response() any {
	return l.response
}

// errorFuture — future с заранее известной ошибкой.
type errorFuture struct {
	err error
}

func (e errorFuture) Error() error  { return e.err }
func (e errorFuture) Index() int    { return 0 }
func (e errorFuture) Response() any { return nil }
