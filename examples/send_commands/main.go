package main

import (
	"log"
	"net"

	vp "github.com/quangd42/viscaoverip"
)

func main() {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:52381")
	if err != nil {
		log.Fatal(err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Fatal(err)
	}

	c, err := vp.NewCamera(conn)
	if err != nil {
		log.Fatal(err)
	}
	// Moves the camera to home position
	err = c.SendCommand("06 04")
	if err != nil {
		log.Fatal(err)
	}
}
