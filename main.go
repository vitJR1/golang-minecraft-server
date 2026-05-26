package main

import (
	"fmt"
	"minecraft-server/server"
	"net"
)

func main() {
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
		go server.HandleConn(conn)
	}
}
