package main

import (
	"log"
	"maps"
	"slices"
	"time"

	"vskurikhin/raft/internal/config"
	"vskurikhin/raft/internal/raft"
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

	ns := raft.NewServer(values.Number, nums, ready)
	go func() {
		for _, num := range nums {
			err := ns.ConnectToPeer(num, values.Peers[num])
			for i := 1; i < 9 && err != nil; i++ {
				log.Printf("try number: %d, error connecting to peer %d: %v", i, num, err)
				time.Sleep(time.Duration(i*500) * time.Millisecond)
				err = ns.ConnectToPeer(num, values.Peers[num])
			}

			if err != nil {
				log.Fatalf("failed to connect to peer %d: %v", num, err)
			}
		}
	}()
	ns.Serve(values.Address.String())
	<-done
	return nil
}
