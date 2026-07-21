package main

import (
	"log"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/vskurikhin/raft/internal/config"
	"github.com/vskurikhin/raft/pkg/kvservice"
	"github.com/vskurikhin/raft/pkg/raft"
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

func run() error {
	return runWith(config.ParseFlags())
}

var wg sync.WaitGroup

func runWith(values config.Values) error {
	nums := slices.Collect(maps.Keys(values.Peers))
	done := make(chan any)
	ready := make(chan any)

	cfg := kvservice.Config{
		HTTPAddress: values.HTTPAddress.String(),
		Config: raft.Config{
			PeerAddresses: values.Peers,
			PeerIds:       nums,
			RPCAddress:    values.RPCAddress.String(),
			ServerID:      values.Number,
		},
	}
	storage := raft.NewMapStorage()
	kvs := kvservice.New(cfg, storage, ready)
	wg.Add(len(nums) / 2)
	for _, num := range nums {
		go connect(num, kvs, values, nums)
	}
	wg.Wait()
	close(ready)
	kvs.ServeHTTP(values.HTTPAddress.String())
	<-done
	return nil
}

var (
	count int
	mu    sync.Mutex
)

func connect(n int, kvs *kvservice.KVService, values config.Values, nums []int) {
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
