# Kubernetes

Air-Mixer runs on Kubernetes with the Helm chart in
[`deploy/helm/airmixer`](../deploy/helm/airmixer). The chart is built
for one setup in particular: speakers paired over Bluetooth with a node
other than the one Air-Mixer runs on, such as a Raspberry Pi that joins
the cluster only to drive the speakers in one room.

## How it fits together

```text
 iPhone ──AirPlay──▶ controller ──AirPlay over the network──▶ room agent ──PulseAudio──▶ Bluetooth speakers
                     (any node)                               (the Pi)      (host)
```

- The **controller** is the main Air-Mixer: the web UI, the database and
  the AirPlay receivers your devices play to. It runs as a Deployment.
- Each **room** runs an **agent**: a second Air-Mixer on the node its
  speakers are paired with, run as a DaemonSet pinned to that node. It
  appears as one AirPlay receiver named after the room, and plays what it
  receives on every speaker in the room.
- The chart adds each room to the controller as an AirPlay output,
  addressed through a headless Service, so you route to "Living Room"
  like any other speaker.

The agent plays to each speaker with the `pulse` output, which talks to
the host's PipeWire or PulseAudio server through its socket. Bluetooth
stays on the host: the pod needs no Bluetooth adapter, D-Bus or
privileges.

Fanning out on the Pi, rather than sending a stream to each speaker,
means the speakers in a room share one clock, so they drift apart far
less. What is left is each speaker's own Bluetooth latency, which
`delayMs` evens out.

## Prepare the Raspberry Pi

These steps use Raspberry Pi OS (Bookworm or later) and its default user
`pi` with uid 1000. A Pi 4 or 5 has plenty of CPU for this.

1. Join the cluster, for example as a k3s agent, then keep other
   workloads off the node:

   ```bash
   kubectl taint nodes pi-living-room airmixer.yeix.io/room=living-room:NoSchedule
   ```

2. Install PipeWire with Bluetooth support, and keep the user session
   running without a login:

   ```bash
   sudo apt install pipewire pipewire-pulse wireplumber libspa-0.2-bluetooth bluez
   sudo loginctl enable-linger pi
   systemctl --user enable --now pipewire pipewire-pulse wireplumber
   ```

3. WirePlumber only starts Bluetooth audio for a logged-in seat by
   default. On a headless Pi, turn that off. For WirePlumber 0.5, create
   `~/.config/wireplumber/wireplumber.conf.d/80-headless.conf`:

   ```text
   wireplumber.profiles = {
     main = {
       monitor.bluez.seat-monitoring = disabled
     }
   }
   ```

   For WirePlumber 0.4, create
   `~/.config/wireplumber/bluetooth.lua.d/80-headless.lua` with
   `bluez_monitor.properties["with-logind"] = false`. Then run
   `systemctl --user restart wireplumber`.

4. Pair and trust each speaker, so it reconnects when it is switched on:

   ```bash
   bluetoothctl
   [bluetooth]# scan on
   [bluetooth]# pair AA:BB:CC:DD:EE:01
   [bluetooth]# trust AA:BB:CC:DD:EE:01
   [bluetooth]# connect AA:BB:CC:DD:EE:01
   ```

5. Check that each speaker has a sink:

   ```bash
   pactl list short sinks
   # bluez_output.AA_BB_CC_DD_EE_01.1  ...
   ```

## Install the chart

Start from [`examples/raspberry-pi-room.yaml`](../deploy/helm/airmixer/examples/raspberry-pi-room.yaml):

```yaml
controller:
  setup:
    servers:
      - name: Air-Mixer          # what your phone plays to
        protocol: airplay
    routes:
      - name: Air-Mixer to Living Room
        source: Air-Mixer
        destinations: [Living Room]

rooms:
  - name: living-room
    displayName: Living Room
    nodeSelector:
      kubernetes.io/hostname: pi-living-room
    tolerations:
      - key: airmixer.yeix.io/room
        operator: Equal
        value: living-room
        effect: NoSchedule
    speakers:
      - name: Living Room Left
        sink: "AA:BB:CC:DD:EE:01"
      - name: Living Room Right
        sink: "AA:BB:CC:DD:EE:02"
        delayMs: 40
```

```bash
helm install airmixer deploy/helm/airmixer -f living-room.yaml
kubectl port-forward svc/airmixer 9001   # then open http://localhost:9001
```

Pick "Air-Mixer" in Control Center on an iPhone and both speakers play.
Routes, volumes and other speakers can still be managed in the web UI;
what the chart declares is applied again on every restart.

A speaker's `sink` can be its PulseAudio sink name, its description, or
its Bluetooth address. While a speaker is off, its output shows as
`connecting` in `/api/status` and the agent keeps retrying; when the
speaker drops off mid-song, the agent notices within a few seconds and
picks it up again when it comes back.

## Phones playing over Bluetooth

The controller can also be a Bluetooth speaker that phones pair with,
through a `bluetooth` input. Run it on a node prepared as in steps 2 and
3 above, pin the controller there, and give it the host's audio socket
and D-Bus system bus:

```yaml
controller:
  nodeSelector:
    kubernetes.io/hostname: pi-living-room
  hostAudio:
    enabled: true
    uid: 1000              # the user running PipeWire
    gid: 1000
    bluetooth: true
    bluetoothGid: "112"    # getent group bluetooth
  setup:
    servers:
      - name: Living Room
        protocol: bluetooth
```

Air-Mixer makes the adapter discoverable as "Living Room" and accepts
pairings without a PIN, for audio only. BlueZ only lets members of its
group, or root, register a pairing agent; if pairing stays off,
`/api/status` gives BlueZ's reason. `hostAudio` without `bluetooth`
is enough for a `pulse` input recording a line-in or USB interface.

## Lining speakers up

Bluetooth speakers each add their own delay, often between 100 and
250 ms, and in one room the difference is heard as an echo. Give the
speakers that play earliest a `delayMs`, raising it until the echo goes
away; a clap or a click track makes this easy to hear. Changing it with
`helm upgrade` restarts the room's agent.

## Values

The most useful values are below; [`values.yaml`](../deploy/helm/airmixer/values.yaml)
has all of them with comments.

| Value | Default | Meaning |
|-------|---------|---------|
| `image.repository` | `ghcr.io/yeixio/air-mixer` | Multi-arch image (amd64, arm64, armv7) |
| `controller.hostNetwork` | `true` | Needed to advertise AirPlay receivers and discover speakers over mDNS |
| `controller.airplayPortRange` | 48 ports from `controller.airplayPort` (49152-49199) | TCP ports the controller's AirPlay inputs take, in order, as `first-last` |
| `controller.setup` | `{}` | Servers, clients and routes for the controller, as in the config file's `setup` section |
| `controller.persistence.size` | `1Gi` | The database volume; kept on uninstall |
| `controller.hostAudio.enabled` | `false` | Mount the host's PipeWire/PulseAudio socket, for `pulse` and `bluetooth` inputs; the controller then runs as `hostAudio.uid` |
| `controller.hostAudio.bluetooth` | `false` | Also mount the D-Bus system bus, so `bluetooth` inputs can accept pairings |
| `controller.hostAudio.bluetoothGid` | | Host group allowed to use BlueZ, added to the pod's groups |
| `rooms[].name` | | Kubernetes name of the room, a DNS label |
| `rooms[].displayName` | `name` | AirPlay name of the room |
| `rooms[].nodeSelector` | | Required: picks the node the speakers are paired with |
| `rooms[].speakers[]` | | `name`, `sink` and optional `delayMs` for each speaker |
| `roomDefaults.airplayPort` | `49200` | The agent's AirPlay port; rooms sharing a node need different ports |
| `roomDefaults.pulse.uid` | `1000` | Host user running PipeWire; the agent runs as this user |
| `roomDefaults.pulse.socketDir` | `/run/user/<uid>/pulse` | Host directory holding the `native` socket |
| `roomDefaults.pulse.cookie` | | Host path of PulseAudio's cookie, if the server needs one (PipeWire does not) |
| `roomDefaults.registerWithController` | `true` | Add the room to the controller as an AirPlay output |

## Limits

- **Bluetooth bandwidth.** The Pi's built-in radio plays to about two or
  three speakers at once and shares its antenna with Wi-Fi. Use Ethernet
  for the Pi, and add a USB Bluetooth adapter for more speakers.
- **Rooms are not in step with each other.** Speakers within a room share
  a clock; separate rooms each buffer on their own, so walking between
  rooms you may hear them slightly apart. Synchronised multi-room playback
  is on the [roadmap](../ROADMAP.md).
- **Network jitter.** The AirPlay receiver has no jitter buffer yet, so a
  busy Wi-Fi link between the controller and the Pi can cause clicks.
  Wired Ethernet avoids it.
- **One controller.** The database is one SQLite file, so the controller
  runs a single replica.
