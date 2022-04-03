package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"golang.org/x/oauth2"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

// main ...
func main() {
	var target string

	flag.StringVar(&target, "target", "", "[serverip:port]")
	flag.Parse()

	if target == "" {
		fmt.Printf("Enter Server: ")
		reader := bufio.NewReader(os.Stdin)
		target, _ = reader.ReadString('\n')
		target = strings.Replace(target, "\n", "", -1)
		target = strings.Replace(target, "\r", "", -1)
	}

	if len(strings.Split(target, ":")) == 1 {
		target += ":19132"
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	token := GetToken()
	src := auth.RefreshTokenSource(&token)

	p, err := minecraft.NewForeignStatusProvider(target)
	if err != nil {
		panic(err)
	}

	listener, err := minecraft.ListenConfig{StatusProvider: p}.Listen("raknet", "127.0.0.1:19132")
	if err != nil {
		panic(err)
	}

	defer func(listener *minecraft.Listener) {
		err := listener.Close()
		if err != nil {
			panic(err)
		}
	}(listener)

	for {
		c, err := listener.Accept()
		if err != nil {
			panic(err)
		}

		go HandleProxy(c.(*minecraft.Conn), listener, src, target)
	}
}

// HandleProxy ...
func HandleProxy(conn *minecraft.Conn, listener *minecraft.Listener, src oauth2.TokenSource, target string) {
	serverConn, err := minecraft.Dialer{
		TokenSource: src,
	}.Dial("raknet", target)
	if err != nil {
		panic(err)
	}

	var g sync.WaitGroup
	g.Add(2)

	go func() {
		if err := conn.StartGame(serverConn.GameData()); err != nil {
			panic(err)
		}
		g.Done()
	}()

	go func() {
		if err := serverConn.DoSpawn(); err != nil {
			panic(err)
		}
		g.Done()
	}()

	g.Wait()

	go func() {
		defer func(listener *minecraft.Listener, conn *minecraft.Conn, message string) {
			err := listener.Disconnect(conn, message)
			if err != nil {
				panic(err)
			}
		}(listener, conn, "disconnected")

		defer func(serverConn *minecraft.Conn) {
			err := serverConn.Close()
			if err != nil {
				panic(err)
			}
		}(serverConn)
		for {
			pk, err := conn.ReadPacket()

			if err != nil {
				panic(err)
			}

			if err := serverConn.WritePacket(pk); err != nil {
				if disconnect, ok := errors.Unwrap(err).(minecraft.DisconnectError); ok {
					_ = listener.Disconnect(conn, disconnect.Error())
				}
				return
			}

			// Client side here, so we don't need to handle any incoming packets.
		}
	}()

	go func() {
		defer func(serverConn *minecraft.Conn) {
			err := serverConn.Close()
			if err != nil {
				panic(err)
			}
		}(serverConn)

		defer func(listener *minecraft.Listener, conn *minecraft.Conn, message string) {
			err := listener.Disconnect(conn, message)
			if err != nil {
				panic(err)
			}
		}(listener, conn, "disconnected")
	}()

	for {

		pk, err := serverConn.ReadPacket()
		if err != nil {
			if disconnect, ok := errors.Unwrap(err).(minecraft.DisconnectError); ok {
				_ = listener.Disconnect(conn, disconnect.Error())
			}
			return
		}

		if err := conn.WritePacket(pk); err != nil {
			return
		}

		// Write Packets here.
	}
}

// TokenFile ...
const TokenFile = "token.json"

// GetToken ...
func GetToken() oauth2.Token {
	var token oauth2.Token
	var err error

	if _, err = os.Stat(TokenFile); err == nil {
		f, err := os.Open(TokenFile)
		if err != nil {
			panic(err)
		}

		defer func(f *os.File) {
			_ = f.Close()
		}(f)
		if err := json.NewDecoder(f).Decode(&token); err != nil {
			panic(err)
		}
	} else {
		_token, err := auth.RequestLiveToken()
		if err != nil {
			panic(err)
		}

		token = *_token

		buf, err := json.Marshal(token)
		if err != nil {
			panic(err)
		}

		_ = os.WriteFile(TokenFile, buf, 0666)
	}
	return token
}
