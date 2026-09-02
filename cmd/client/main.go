package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/vskurikhin/raft/pkg/kvclient"
)

const (
	MinimalDuration = 750
)

func main() {
	client := kvclient.New([]string{"192.168.21.223:8880", "192.168.21.222:8880", "192.168.21.221:8880"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(MinimalDuration)*time.Millisecond)
	defer cancel()
	if len(os.Args) > 2 && os.Args[1] == "get" {
		value, ok, err := client.Get(ctx, os.Args[2])
		if err != nil {
			//nolint:gocritic
			log.Fatalf(`{"Error", "%s"}`, err)
		}
		fmt.Printf(`{"RespStatus":1, "KeyFound":%v, "Value":"%s"}`, ok, value)
	}
	if len(os.Args) > 2 && os.Args[1] == "put" {
		result, ok, err := client.Put(ctx, os.Args[2], os.Args[3])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf(`{"RespStatus":1, "KeyFound":%v, "PrevValue":"%s"}`, ok, result)
	}
}
