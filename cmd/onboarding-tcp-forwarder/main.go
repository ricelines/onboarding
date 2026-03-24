package main

import (
	"flag"
	"io"
	"log"
	"net"
	"sync"
)

func main() {
	var listenAddr string
	var targetAddr string

	flag.StringVar(&listenAddr, "listen", "", "local listen address")
	flag.StringVar(&targetAddr, "target", "", "upstream target address")
	flag.Parse()

	if listenAddr == "" || targetAddr == "" {
		log.Fatal("--listen and --target are required")
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", listenAddr, err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatalf("accept %s: %v", listenAddr, err)
		}
		go proxyConn(conn, targetAddr)
	}
}

func proxyConn(src net.Conn, target string) {
	dst, err := net.Dial("tcp", target)
	if err != nil {
		_ = src.Close()
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		closeWrite(dst)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(src, dst)
		closeWrite(src)
	}()
	wg.Wait()
	_ = src.Close()
	_ = dst.Close()
}

func closeWrite(conn net.Conn) {
	type closeWriter interface {
		CloseWrite() error
	}
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}
