package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vskurikhin/raft/internal/load/config"
	"github.com/vskurikhin/raft/pkg/kvclient"
)

const (
	Workers        = 4
	RequestTimeout = 10 * time.Second
)

var (
	keys []string

	getOK     atomic.Uint64
	getFail   atomic.Uint64
	putOK     atomic.Uint64
	putFail   atomic.Uint64
	verifyOK  atomic.Uint64
	verifyBad atomic.Uint64

	reqCount  atomic.Uint64
	latencyCh = make(chan time.Duration, 4096)

	values config.Values
)

//nolint:gochecknoinits
func init() {
	values = config.ParseFlags()
	keys = make([]string, values.KeyCount)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}
}

func main() {
	peers := make([]string, 0, len(values.Peers))
	for _, peer := range values.Peers {
		peers = append(peers, peer.String())
	}
	client := kvclient.New(peers)
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()
	interval := time.Second / time.Duration(values.RequestRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var wg sync.WaitGroup
	for i := range Workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					//nolint:contextcheck
					run(client, r)
				}
			}
		}(i)
	}
	go stats(ctx)
	wg.Wait()
}

func run(client *kvclient.KVClient, rnd *rand.Rand) {
	start := time.Now()
	defer func() {
		latencyCh <- time.Since(start)
	}()
	key := keys[rnd.Intn(len(keys))]
	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()
	if rnd.Intn(100) < values.GetPercent {
		_, _, err := client.Get(ctx, key)
		if err != nil {
			getFail.Add(1)
			fmt.Printf("GET: %v\n", err)
			return
		}
		getOK.Add(1)
		reqCount.Add(1)
		return
	}
	value := fmt.Sprintf("value-%d", time.Now().UnixNano())
	_, _, err := client.Put(ctx, key, value)
	if err != nil {
		putFail.Add(1)
		reqCount.Add(1)
		fmt.Printf("PUT: %v\n", err)
		return
	}
	putOK.Add(1)
	if rnd.Intn(100) < values.VerifyPercent {
		got, ok, err := client.Get(ctx, key)
		reqCount.Add(1)
		if err != nil || !ok || got != value {
			verifyBad.Add(1)
		} else {
			verifyOK.Add(1)
		}
	}
	reqCount.Add(1)
}

func stats(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var prev uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := reqCount.Load()
			rps := cur - prev
			prev = cur
			var latencies []float64
		drain:
			for {
				select {
				case d := <-latencyCh:
					latencies = append(latencies, float64(d.Microseconds())/1000.0)
				default:
					break drain
				}
			}
			p50, p80, p95, p99 := 0.0, 0.0, 0.0, 0.0
			if n := len(latencies); n > 0 {
				sort.Float64s(latencies)
				p50 = latencies[n*50/100]
				p80 = latencies[n*80/100]
				p95 = latencies[n*95/100]
				p99 = latencies[n*99/100]
			}
			msg := fmt.Sprintf(
				"RPS=%5d  p50=%7.2fms p80=%7.2fms p95=%7.2fms p99=%7.2fms  GET ok=%8d fail=%4d  PUT ok=%8d fail=%4d  VERIFY ok=%6d bad=%4d\n",
				rps, p50, p80, p95, p99,
				getOK.Load(),
				getFail.Load(),
				putOK.Load(),
				putFail.Load(),
				verifyOK.Load(),
				verifyBad.Load(),
			)
			slog.Info(msg)
		}
	}
}
