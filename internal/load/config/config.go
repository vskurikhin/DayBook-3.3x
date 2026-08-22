// Package config Разбор аргументов командной строки
package config

import (
	"flag"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	PrefixHTTP = "http://"
)

type Values struct {
	// Concurrency — предельное число одновременно выполняющихся запросов.
	Concurrency int
	// Duration — длительность прогона; ноль означает работу до сигнала.
	Duration   time.Duration
	GetPercent int
	KeyCount   int
	Peers      []net.Addr
	// RequestRate — число тиков в секунду, задающих моменты старта запросов.
	RequestRate int
	// ValueSize — размер значения в байтах для операций записи.
	ValueSize     int
	VerifyPercent int
}

func ParseFlags() Values {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	concurrencyFlag := fs.Int("concurrency", 4, "Maximum number of requests in flight")
	durationFlag := fs.Duration("duration", 0, "Load run duration (0 = until signal)")
	getPercentFlag := fs.Int("get-percent", 66, "")
	keyCountFlag := fs.Int("key-count", 2000, "")
	requestRateFlag := fs.Int("request-rate", 100, "")
	peersFlag := fs.String("peers", "", "Comma-separated list of peers servers (host:port)")
	valueSizeFlag := fs.Int("value-size", 128, "Value size in bytes")
	verifyPercentFlag := fs.Int("verify-percent", 33, "")

	args := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-test.") {
			continue
		}
		args = append(args, arg)
	}
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}

	peers := make([]net.Addr, 0)
	if *peersFlag != "" {
		peers = parsePeers(peers, *peersFlag)
	}

	return Values{
		Concurrency:   *concurrencyFlag,
		Duration:      *durationFlag,
		GetPercent:    *getPercentFlag,
		KeyCount:      *keyCountFlag,
		RequestRate:   *requestRateFlag,
		Peers:         peers,
		ValueSize:     *valueSizeFlag,
		VerifyPercent: *verifyPercentFlag,
	}
}

func parsePeers(peers []net.Addr, raw string) []net.Addr {
	for addr := range strings.SplitSeq(raw, ",") {
		if addr == "" {
			log.Fatalf("invalid peer server address: %s", raw)
		}
		peers = addrAppend(peers, addr)
	}
	return peers
}

func addrAppend(peers []net.Addr, addr string) []net.Addr {
	addr = strings.TrimSpace(addr)

	// Check if address has a scheme prefix

	if strings.HasPrefix(addr, PrefixHTTP) {
		if _, err := url.Parse(addr); err != nil {
			log.Fatalf("invalid peer address: %s", addr)
		}
		peers = append(peers, parseHTTPAddress(addr))
		return peers
	}

	// No scheme — assume http:// (backward compatibility)
	withScheme := PrefixHTTP + addr
	if _, err := url.Parse(withScheme); err != nil {
		log.Fatalf("invalid peer address: %s", addr)
	}
	peers = append(peers, parseHTTPAddress(withScheme))
	return peers
}

func parseHTTPAddress(addr string) net.Addr {
	var result net.Addr
	trimmed, _ := strings.CutPrefix(addr, PrefixHTTP)
	// Преобразуем строку в net.Addr
	result, err := net.ResolveTCPAddr("tcp", trimmed)
	if err != nil {
		log.Fatalf("invalid peers address: %s", addr)
	}
	return result
}
