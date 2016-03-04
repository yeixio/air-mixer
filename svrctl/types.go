package svrctl

type serverobj struct {
	listener string
	name     string
	iface    string
}

type servers struct {
	server map[string]serverobj
}
