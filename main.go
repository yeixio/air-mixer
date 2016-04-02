package main

import (
	"os"

	"github.com/codegangsta/cli"
	"github.com/spankenstein/air-mixer/svrctl"
	"github.com/spankenstein/air-mixer/webui"
)

func main() {
	publishName := ""
	listener := ""
	iface := ""
	app := cli.NewApp()
	app.Name = "airmixer"
	app.Usage = "A tool to convert one streaming type to another."
	app.Version = "0.0.1"

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "name, n",
			Value:       "airmixer",
			Usage:       "Network publish name of mixer.",
			Destination: &publishName,
		},
		cli.StringFlag{
			Name:        "listener, l",
			Value:       ":49152",
			Usage:       "Listener for server. Default (:49152)",
			Destination: &listener,
		},
		cli.StringFlag{
			Name:        "interface, i",
			Value:       "auto",
			Usage:       "Airmixer will automatically try to find the main interface. It will alert if it finds more then one.",
			Destination: &iface,
		},
	}

	app.Commands = []cli.Command{
		{
			Name:    "start",
			Aliases: []string{"s"},
			Usage:   "Starts single airmixer instance.",
			Action: func(c *cli.Context) {
				srvr := svrctl.New(publishName)
				srvr.StartServer()
			},
		},
		{
			Name:    "server",
			Aliases: []string{"d"},
			Usage:   "Starts airmixer server",
			Action: func(c *cli.Context) {
				webui.LaunchWebUI()
			},
		},
	}

	app.Run(os.Args)
}
