package apserver

import (
	"encoding/hex"
	"log"
	"math/rand"
	"net"
	"strconv"
)

var txt map[string]string = map[string]string{
	"txtvers": "1",
	"pw":      "false",
	"tp":      "UDP",
	"sm":      "false",
	"ek":      "1",
	"cn":      "0,1",
	"ch":      "2",
	"ss":      "16",
	"sr":      "44100",
	"vn":      "3",
	"et":      "0,1",
}

func sessionHandler(id string, conn net.Conn) {
	rtspSession(id, conn, func(x chan string) {})
}

func ServeAirPlay(name string, intface string, address string) error {
	//address := ":49152"

	// Try to grab publish information
	_, portstr, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portstr)
	if err != nil {
		return err
	}
	iface, err := net.InterfaceByName(intface)
	if err != nil {
		return err
	}

	// Publish the service
	raopName := hex.EncodeToString(iface.HardwareAddr) + "@" + name
	err = ServiceRegister(raopName, "_raop._tcp", txt, uint16(port))
	if err != nil {
		return err
	}
	defer ServiceDeregister()
	log.Println("Service", raopName, "registered on address", address)

	// Bind the port
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Println("Listening for connections on address", address)

	// Listen for incoming
	for {
		id := strconv.Itoa(rand.Int())
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go sessionHandler(id, conn)
	}
}
