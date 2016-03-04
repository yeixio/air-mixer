package svrctl

type serverobj struct {
	listener string
	name     string
	iface    string
}

type clientobj struct {
	clientType  string
	name        string
	destination string
}

type servers struct {
	server map[string]serverobj
}

type routes struct {
	server  serverobj
	clients map[string]clientobj
}
