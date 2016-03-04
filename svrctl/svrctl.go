package svrctl

import (
	"errors"
	"log"
	"net"

	"github.com/spankenstein/airmixer/server"
)

func getMainInterface() (string, error) {
	var count int
	var interfaceName string

	interfaces, err := net.Interfaces()
	if err != nil {
		log.Fatalln("ERROR:", err)
	}

	for _, i := range interfaces {
		addrs, err := i.Addrs()
		if err != nil {
			log.Fatalln("ERROR:", err)
		}
		for _, address := range addrs {
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					count++
					interfaceName = i.Name
				}

			}
		}

	}
	if count > 1 {
		return "", errors.New("More then one interface detected. Please specify interface name you would like to use.")
	}
	return interfaceName, nil
}

func StartServer(publishName string) {
	address := ":49152"
	insterfaceName, err := getMainInterface()
	if err != nil {
		log.Fatalln(err)
	}
	log.Println("Main interface detected:", insterfaceName)
	log.Println("Starting server with name:", publishName)
	err = server.ServeAirPlay(publishName, insterfaceName, address)
	if err != nil {
		log.Println(err)
	}

}
