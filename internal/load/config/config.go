// Package config Разбор аргументов командной строки
package config

import (
	"flag"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
)

const (
	PrefixHTTP = "http://"
)

type Values struct {
	GetPercent    int
	KeyCount      int
	Peers         []net.Addr
	RequestRate   int
	VerifyPercent int
}

func ParseFlags() Values {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	getPercentFlag := fs.Int("get-percent", 66, "")
	keyCountFlag := fs.Int("key-count", 2000, "")
	requestRateFlag := fs.Int("request-rate", 100, "")
	peersFlag := fs.String("peers", "", "Comma-separated list of peers servers (host:port)")
	verifyPercentFlag := fs.Int("verify-percent", 33, "")
	err := fs.Parse(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	peers := make([]net.Addr, 0)
	if *peersFlag != "" {
		peers = parsePeers(peers, *peersFlag)
	}

	return Values{
		GetPercent:    *getPercentFlag,
		KeyCount:      *keyCountFlag,
		RequestRate:   *requestRateFlag,
		Peers:         peers,
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
