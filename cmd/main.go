package main

import (
	"fmt"
	"log"
	"maps"
	"path/filepath"
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

var wg sync.WaitGroup

// runWith создаёт и запускает узел с заданными параметрами values и
// возвращает stop-функцию для корректного завершения узла
// (CM.Stop → HTTP shutdown). Позволяет тестам управлять полным
// жизненный цикл узла без глобальных экземпляров запуска.
func runWith(values *config.Values) (func(), error) {
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
		return nil, fmt.Errorf("failed to create file snapshot store in %s: %w", dataDir, err)
	}

	cfg := kvservice.Config{
		HTTPAddress: values.HTTPAddress.String(),
		Config: raft.Config{
			PeerAddresses: values.Peers,
			PeerIds:       nums,
			RPCAddress:    values.RPCAddress.String(),
			ServerID:      values.Number,
			SnapshotStore: snapshotStore,
			Storage:       raft.NewFileStorage(dataDir),
			TCPRPCTimeout: raft.TCPRPCTimeout,
			MaxPool:       4,
		},
	}
	kvs := kvservice.New(&cfg, ready)
	wg.Add(len(nums) / 2)
	for _, num := range nums {
		go connect(num, kvs, values, nums)
	}
	wg.Wait()
	close(ready)
	if err := kvs.ServeHTTP(values.HTTPAddress.String()); err != nil {
		if shutdownErr := kvs.Shutdown(); shutdownErr != nil {
			log.Printf("warning: shutting down node %d: %v", values.Number, shutdownErr)
		}
		return nil, fmt.Errorf("failed to serve HTTP on %s: %w", values.HTTPAddress, err)
	}

	return func() {
		log.Printf("stopping node %d", values.Number)
		if err := kvs.Shutdown(); err != nil {
			log.Printf("warning: shutting down node %d: %v", values.Number, err)
		}
	}, nil
}

var (
	count int
	mu    sync.Mutex
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
	} else if count < len(nums)/2 {
		mu.Lock()
		count++
		wg.Done()
		mu.Unlock()
	}
}
