package apserver

import (
	"encoding/hex"
	"math/rand"
	"net"
	"strconv"

	"github.com/yeixio/air-mixer/internal/logger"
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

// ANNOUNCEHandler is called when ANNOUNCE message is received
type ANNOUNCEHandler func(sessionID string, fmtp []int, aesiv, aeskey []byte) error

var globalANNOUNCEHandler ANNOUNCEHandler

func sessionHandler(id string, conn net.Conn) {
	rtspSession(id, conn, func(x chan string) {})
}

func ServeAirPlay(name string, intface string, address string) (net.Listener, error) {
	ln, err := ServeAirPlayWithHandler(name, intface, address, nil)
	return ln, err
}

// ServeAirPlayWithHandler starts an AirPlay server that can be stopped by closing the listener
// Returns the listener so the caller can close it to stop the server
func ServeAirPlayWithHandler(name string, intface string, address string, handler ANNOUNCEHandler) (net.Listener, error) {
	log := logger.GetLogger()
	log.Debugf("ServeAirPlayWithHandler called - name: %s, interface: %s, address: %s", name, intface, address)
	
	globalANNOUNCEHandler = handler

	// Try to grab publish information
	_, portstr, err := net.SplitHostPort(address)
	if err != nil {
		log.Errorf("Failed to split host:port from address '%s': %v", address, err)
		return nil, err
	}
	port, err := strconv.Atoi(portstr)
	if err != nil {
		log.Errorf("Failed to parse port '%s': %v", portstr, err)
		return nil, err
	}
	log.Debugf("Parsed port: %d", port)
	
	iface, err := net.InterfaceByName(intface)
	if err != nil {
		log.Errorf("Failed to get interface '%s': %v", intface, err)
		return nil, err
	}
	log.Debugf("Interface '%s' found - MAC: %s, MTU: %d", intface, hex.EncodeToString(iface.HardwareAddr), iface.MTU)

	// Publish the service
	raopName := hex.EncodeToString(iface.HardwareAddr) + "@" + name
	log.Infof("Registering AirPlay service: name='%s', type='_raop._tcp', port=%d", raopName, port)
	log.Debugf("Service TXT record: %v", txt)
	
	err = ServiceRegister(raopName, "_raop._tcp", txt, uint16(port))
	if err != nil {
		log.Errorf("Failed to register mDNS service '%s': %v", raopName, err)
		return nil, err
	}
	// Note: mDNS deregistration will happen when listener is closed
	log.Infof("✓ mDNS service '%s' registered successfully on address %s (port %d)", raopName, address, port)

	// Bind the port
	log.Debugf("Binding TCP listener on address: %s", address)
	ln, err := net.Listen("tcp", address)
	if err != nil {
		log.Errorf("Failed to bind TCP listener on '%s': %v", address, err)
		// Deregister mDNS if listener fails
		ServiceDeregister()
		return nil, err
	}
	log.Infof("✓ TCP listener bound successfully on address %s", address)

	// Listen for incoming in a goroutine
	go func() {
		log.Infof("AirPlay server ready - waiting for connections on %s", address)
		for {
			conn, err := ln.Accept()
			if err != nil {
				// Check if error is due to listener being closed
				if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
					log.Debugf("TCP listener closed for '%s'", name)
					ServiceDeregister() // Deregister mDNS when listener closes
					return
				}
				log.Errorf("Error accepting connection: %v", err)
				ServiceDeregister() // Deregister mDNS on error
				return
			}
			id := strconv.Itoa(rand.Int())
			log.Debugf("Accepted new connection (session: %s) from %s", id, conn.RemoteAddr())
			go sessionHandler(id, conn)
		}
	}()

	return ln, nil
}
