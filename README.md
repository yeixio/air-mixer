# air-mixer

## Current State:
Right now this will simply configure a single airplay server.  
`go get github.com/spankenstein/air-mixer`


```
NAME:
   airmixer - A tool to convert one streaming type to another.

USAGE:
   airmixer [global options] command [command options] [arguments...]

VERSION:
   0.0.1

COMMANDS:
   start, s	Starts airmixer server
   help, h	Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --name, -n "airmixer"	Network publish name of mixer.
   --listener, -l ":49152"	Listener for server. Default (:49152)
   --interface, -i "auto"	Airmixer will automatically try to find the main interface. It will alert if it finds more then one.
   --help, -h			show help
   --version, -v		print the version
```

## Goal
The plan with this project is to build a translator app that can convert various
audio stream methods to others. i.e. Airplay -> ChromeCast, ChromeCast -> upnp or
any combo. I also plan on having the ability to split one stream into multi targets.
i.e. Airplay to a airplay, ChromeCast target at the same time.    

## TODO:

- Server objects: The ability to create separate server listeners.
- Client objects: The ability to define a target.
- Cache object: for a client to be able to read a stream from a mismatched server.
- WebInterface to manage server objects and link servers with targets.
- Move off of the C libs for CoreAudio interface. 
