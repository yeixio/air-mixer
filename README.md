# air-mixer

## Current State:
Right now this will simply configure a single airplay server.  

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
