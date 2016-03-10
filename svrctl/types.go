package svrctl

type Serverobj struct {
	listener string
	name     string
	iface    string
}

type Clientobj struct {
	clientType  string
	name        string
	destination string
}

type Servers struct {
	server map[string]Serverobj
}

type Routes struct {
	server  Serverobj
	clients map[string]Clientobj
}
