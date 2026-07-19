package main

import (
	"log"
	"maps"
	"slices"
	"sync/atomic"
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

func runWith(values config.Values) error {
	nums := slices.Collect(maps.Keys(values.Peers))
	done := make(chan any)
	ready := make(chan any)
	count := atomic.Int64{}

	storage := raft.NewMapStorage()
	kvs := kvservice.New(values.RPCAddress.String(), values.Number, nums, storage, ready)
	go func() {
		for _, num := range nums {
			err := kvs.ConnectToRaftPeer(num, values.Peers[num])
			for i := 0; i < Try && err != nil; i++ {
				duration := (i * MinimalDuration) % DurationModulus
				time.Sleep(time.Duration(duration+MinimalDuration) * time.Millisecond)
				err = kvs.ConnectToRaftPeer(num, values.Peers[num])
			}

			if err != nil {
				log.Printf("warning connect to peer %d: error: %v", num, err)
				count.Add(1)
				if count.Load() > int64(len(nums)/2) {
					log.Fatalf("failed to connect to peer %d: %v, error: not quorum: %d", num, err, len(nums)/2)
				}
			}
		}
		ready <- true
	}()
	kvs.ServeHTTP(values.HTTPAddress.String())
	<-done
	return nil
}
