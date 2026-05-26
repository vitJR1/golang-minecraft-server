package main

import (
	"fmt"
	"minecraft-server/ban"
	"minecraft-server/server"
	"net"
)

func main() {
	if err := ban.Load("banlist.json"); err != nil {
		fmt.Printf("warning: failed to load banlist.json: %v\n", err)
	}

	srv := server.New()

	lis, err := net.Listen("tcp", ":25565")
	if err != nil {
		panic(err)
	}
	fmt.Println("Listening on :25565 (Minecraft test server)")

	for {
		conn, err := lis.Accept()
		if err != nil {
			fmt.Printf("accept error: %v\n", err)
			continue
		}
		go srv.HandleConn(conn)
	}
}
