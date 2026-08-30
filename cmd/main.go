package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/http/pprof"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/internal/_init"
	"github.com/vskurikhin/raft/internal/config"
	"github.com/vskurikhin/raft/pkg/kvservice"
)

const (
	DurationModulus = 30 * 1000
	MinimalDuration = 500
	Try             = 4096

	// _pprofReadHeaderTimeout — предельное время чтения заголовков запроса
	// сервером профилирования.
	_pprofReadHeaderTimeout = 5 * time.Second
	// _pprofShutdownTimeout — предельное время остановки сервера профилирования.
	_pprofShutdownTimeout = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run запускает узел с параметрами командной строки и блокируется
// до завершения процесса.
func run() error {
	stop, err := runWith(&_init.Values)
	if err != nil {
		return err
	}
	defer stop()
	select {} // работа узла до завершения процесса
}

var _wg sync.WaitGroup

// runWith создаёт и запускает узел с заданными параметрами values и
// возвращает stop-функцию для корректного завершения узла
// (CM.Stop → HTTP shutdown). Позволяет тестам управлять полным
// жизненный цикл узла без глобальных экземпляров запуска.
func runWith(values *config.Values) (func(), error) {
	stopPprof := startPprof(values)
	nums := slices.Collect(maps.Keys(values.Peers))
	ready := make(chan any)

	dataDir := values.DataDir
	if dataDir == "" {
		dataDir = filepath.Join("data", fmt.Sprintf("node-%d", values.Number))
	}

	// Постоянное хранилище снимков: сжатие усекает журнал на диске,
	// поэтому снимок обязан переживать рестарт процесса. retain=2 —
	// запас на случай повреждения последнего снимка.
	snapshotStore, err := raft.NewFileSnapshotStore(dataDir, 2)
	if err != nil {
		stopPprof()
		return nil, fmt.Errorf("failed to create file snapshot store in %s: %w", dataDir, err)
	}

	// Ноль в конфигурации узла означает значение по умолчанию узла,
	// а не значение по умолчанию транспорта.
	maxPool := values.MaxPool
	if maxPool <= 0 {
		maxPool = config.DefaultMaxPool
	}

	cfg := kvservice.Config{
		HTTPAddress: values.HTTPAddress.String(),
		Config: raft.Config{
			PeerAddresses:     values.Peers,
			PeerIds:           nums,
			RPCAddress:        values.RPCAddress.String(),
			ServerID:          values.Number,
			SnapshotStore:     snapshotStore,
			Storage:           raft.NewFileStorage(dataDir),
			TCPRPCTimeout:     values.TCPRPCTimeout,
			MaxPool:           maxPool,
			SnapshotInterval:  values.SnapshotInterval,
			SnapshotThreshold: values.SnapshotThreshold,
		},
	}
	kvs := kvservice.New(&cfg, ready)
	_wg.Add(len(nums) / 2)
	for _, num := range nums {
		go connect(num, kvs, values, nums)
	}
	_wg.Wait()
	close(ready)
	if err := kvs.ServeHTTP(values.HTTPAddress.String()); err != nil {
		if shutdownErr := kvs.Shutdown(); shutdownErr != nil {
			log.Printf("warning: shutting down node %d: %v", values.Number, shutdownErr)
		}
		stopPprof()
		return nil, fmt.Errorf("failed to serve HTTP on %s: %w", values.HTTPAddress, err)
	}

	return func() {
		log.Printf("stopping node %d", values.Number)
		if err := kvs.Shutdown(); err != nil {
			log.Printf("warning: shutting down node %d: %v", values.Number, err)
		}
		stopPprof()
	}, nil
}

// startPprof поднимает отдельный HTTP-сервер профилирования и включает сбор
// профилей блокировок и состязаний за мьютекс, если заданы соответствующие
// параметры. При пустом адресе не создаётся ни слушающего сокета, ни
// накладных расходов среды выполнения. Возвращает функцию остановки.
func startPprof(values *config.Values) func() {
	if values.PprofAddress == "" {
		return func() {}
	}
	if values.BlockProfileRate > 0 {
		runtime.SetBlockProfileRate(values.BlockProfileRate)
	}
	if values.MutexProfileFraction > 0 {
		runtime.SetMutexProfileFraction(values.MutexProfileFraction)
	}
	server := &http.Server{
		Addr:              values.PprofAddress,
		Handler:           pprofMux(),
		ReadHeaderTimeout: _pprofReadHeaderTimeout,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("warning: profiling server on %s: %v", values.PprofAddress, err)
		}
	}()
	log.Printf("profiling server is available at %s", values.PprofAddress)

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), _pprofShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("warning: shutting down profiling server: %v", err)
		}
	}
}

// pprofMux собирает обработчики профилирования в отдельном мультиплексоре:
// профили не попадают в мультиплексор по умолчанию и не делят порт с
// HTTP-интерфейсом KV-сервиса, чтобы не влиять на измеряемый путь.
func pprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

var (
	_count int
	_mu    sync.Mutex
)

func connect(n int, kvs *kvservice.KVService, values *config.Values, nums []int) {
	log.Printf("connect to peer %d", n)
	err := kvs.ConnectToRaftPeer(n, values.Peers[n])
	for i := 0; i < Try && err != nil; i++ {
		duration := (i * MinimalDuration) % DurationModulus
		time.Sleep(time.Duration(duration+MinimalDuration) * time.Millisecond)
		log.Printf("try connect to peer %d", n)
		err = kvs.ConnectToRaftPeer(n, values.Peers[n])
	}
	if err != nil {
		log.Printf("warning connect to peer %d: error: %v", n, err)
	} else if _count < len(nums)/2 {
		_mu.Lock()
		_count++
		_wg.Done()
		_mu.Unlock()
	}
}
