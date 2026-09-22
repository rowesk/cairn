package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rowesk/cairn/internal/cairn"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

var version = "dev"

func run() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-version":
			fmt.Println("cairn " + version)
			return nil
		case "init":
			return initCommand(os.Args[2:])
		}
	}

	data := flag.String("data", "./data", "persistent data directory")
	address := flag.String("listen", "127.0.0.1:8080", "loopback or local Tailscale IP:port")
	publicAddress := flag.String("public-listen", "", "optional share-only loopback IP:port")
	publicBase := flag.String("public-base-url", "", "public origin, for example https://reports.example.com")
	tokenFile := flag.String("publisher-token-file", "", "file containing publisher token, or use CAIRN_PUBLISHER_TOKEN")
	flag.Parse()
	if (*publicAddress == "") != (*publicBase == "") {
		return fmt.Errorf("public-listen and public-base-url must be supplied together")
	}
	if err := checkAddress(*address); err != nil {
		return err
	}
	token := os.Getenv("CAIRN_PUBLISHER_TOKEN")
	if *tokenFile != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			return err
		}
		token = string(data)
		for len(token) > 0 && (token[len(token)-1] == '\n' || token[len(token)-1] == '\r') {
			token = token[:len(token)-1]
		}
	}
	app, err := cairn.Open(cairn.Config{DataDir: *data, PublisherToken: token, PublicBaseURL: *publicBase})
	if err != nil {
		return err
	}
	defer app.Close()
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	makeServer := func(handler http.Handler) *http.Server {
		return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	}
	privateServer := makeServer(app)
	servers := []*http.Server{privateServer}
	listeners := []net.Listener{listener}
	defer func() {
		for _, server := range servers {
			server.Close()
		}
		for _, listener := range listeners {
			listener.Close()
		}
	}()
	if *publicAddress != "" {
		host, _, err := net.SplitHostPort(*publicAddress)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.IsLoopback() {
			return fmt.Errorf("public listener must be loopback")
		}
		if *publicBase == "" {
			return fmt.Errorf("public-base-url is required with public-listen")
		}
		publicListener, err := net.Listen("tcp", *publicAddress)
		if err != nil {
			return err
		}
		servers = append(servers, makeServer(app.PublicHandler()))
		listeners = append(listeners, publicListener)
		log.Printf("Cairn public listener: %s", publicListener.Addr())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, len(servers))
	for i, server := range servers {
		go func() { done <- server.Serve(listeners[i]) }()
	}
	log.Printf("Cairn private listener: %s", listener.Addr())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, server := range servers {
			if err := server.Shutdown(shutdown); err != nil {
				return err
			}
		}
		return nil
	}
}

func checkAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("listen requires an explicit IP and port: %w", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("listen requires an explicit loopback or Tailscale IP")
	}
	if ip.IsLoopback() {
		return nil
	}
	tail4 := netip.MustParsePrefix("100.64.0.0/10")
	tail6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	if !tail4.Contains(ip) && !tail6.Contains(ip) {
		return fmt.Errorf("refusing non-loopback/non-Tailscale listen address")
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err == nil && prefix.Addr() == ip {
			return nil
		}
	}
	return fmt.Errorf("Tailscale listen address is not assigned to this device")
}
