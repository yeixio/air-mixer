# Home Assistant

The Air-Mixer integration lets Home Assistant play audio on Air-Mixer's
outputs: text-to-speech, doorbells, house-wide announcements and alarms.
While an announcement plays, other audio on those outputs is lowered
(ducked) or silenced, and it comes back when the announcement ends.

The integration lives in this repository under
[`custom_components/airmixer`](../custom_components/airmixer) and talks to
the [playback API](API.md#playback).

## Install

With [HACS](https://hacs.xyz): open HACS, choose **Custom repositories**
from the menu, add `https://github.com/yeixio/air-mixer` with the type
**Integration**, then install **Air-Mixer** and restart Home Assistant.

By hand: copy `custom_components/airmixer` into the `custom_components`
folder of your Home Assistant configuration and restart.

Then go to **Settings > Devices & services > Add integration**, pick
**Air-Mixer**, and enter the server's host name or IP address and its web
port (9001 by default). Home Assistant 2025.8 or later is required.

### Networking

Home Assistant tells Air-Mixer a URL and Air-Mixer downloads the audio
itself, so each must be able to reach the other:

- Home Assistant reaches Air-Mixer on its web port.
- Air-Mixer reaches Home Assistant at its **internal URL**, which
  Home Assistant puts in front of text-to-speech and media file links.
  Set it under **Settings > System > Network** to an address Air-Mixer can
  reach, such as `http://192.168.1.10:8123`, not `localhost`.

The playback API has no authentication yet, so keep Air-Mixer's port on a
trusted network.

## Entities

Each Air-Mixer output (client) becomes a media player, as its own device,
plus one **Air-Mixer All outputs** media player for house-wide
announcements. New outputs appear automatically.

| State | Meaning |
|-------|---------|
| `playing` | Something sent through the playback API is playing there |
| `idle` | Nothing from the playback API is playing; music routed by Air-Mixer may be |
| `off` | The output is disabled in Air-Mixer |
| `unavailable` | Air-Mixer cannot be reached, or the output was deleted |

The `connection` attribute is the output's state in Air-Mixer
(`connecting`, `connected` or `failed`), with `connection_error` when there
is one. Stopping a media player stops what was sent through the playback
API; Air-Mixer's own routes keep playing.

The players support **play media**, **announce**, **stop** and **browse
media**, so they appear in the media browser and can be targeted by
text-to-speech.

## Speech

`tts.speak` sends speech as an announcement, which ducks other audio:

```yaml
action: tts.speak
target:
  entity_id: tts.home_assistant_cloud
data:
  media_player_entity_id: media_player.air_mixer_all_outputs
  message: "Dinner is ready"
```

## Play a file or stream

`media_player.play_media` plays a URL or a media source item. Text-to-speech
and calls with `announce: true` duck other audio; anything else plays
alongside it. `extra` can set any [playback option](API.md#playback):
`mode`, `volume`, `duck_level`, `repeat`, `loop`, `max_duration` and
`title`.

```yaml
action: media_player.play_media
target:
  entity_id: media_player.kitchen
data:
  media_content_id: media-source://media_source/local/doorbell.mp3
  media_content_type: music
  announce: true
  extra:
    volume: 0.8
```

Air-Mixer plays MP3 and WAV. Most text-to-speech engines produce MP3 by
default; convert other files before using them.

## Actions

### `airmixer.announce`

Plays on several outputs with one request, so they start together, and
takes every playback option directly.

| Field | | Default |
|-------|-|---------|
| `entity_id` | Air-Mixer media players. The **All outputs** player, or none, means every output. | all |
| `media_content_id` | A URL or media source item (the UI shows a media picker). Required. | |
| `mode` | `duck`, `interrupt` or `mix` | `duck` |
| `volume` | 0 to 1 | 1 |
| `duck_level` | Volume of other audio under `duck` | 0.2 |
| `repeat` | Times to play | 1 |
| `loop` | Repeat until stopped | `false` |
| `max_duration` | Seconds before it stops | |
| `title` | Shown as the media title | |
| `wait` | Finish the action only when playback ends | `false` |

It returns the playbacks it started (`response_variable`).

### `airmixer.stop`

Stops what Home Assistant sent to the given outputs, or to every output.

## Examples

A doorbell on the ground floor:

```yaml
automation:
  - alias: Doorbell
    triggers:
      - trigger: state
        entity_id: binary_sensor.doorbell
        to: "on"
    actions:
      - action: airmixer.announce
        data:
          entity_id:
            - media_player.kitchen
            - media_player.living_room
          media_content_id: media-source://media_source/local/doorbell.mp3
          duck_level: 0.1
```

A smoke alarm everywhere, silencing music, until someone stops it:

```yaml
automation:
  - alias: Smoke alarm
    triggers:
      - trigger: state
        entity_id: binary_sensor.smoke
        to: "on"
    actions:
      - action: airmixer.announce
        data:
          media_content_id: media-source://media_source/local/alarm.wav
          mode: interrupt
          loop: true
          max_duration: 900
  - alias: Smoke alarm cleared
    triggers:
      - trigger: state
        entity_id: binary_sensor.smoke
        to: "off"
    actions:
      - action: airmixer.stop
```

A chime, then speech, in order:

```yaml
script:
  announce_dinner:
    sequence:
      - action: airmixer.announce
        data:
          media_content_id: media-source://media_source/local/chime.mp3
          wait: true
      - action: tts.speak
        target:
          entity_id: tts.piper
        data:
          media_player_entity_id: media_player.air_mixer_all_outputs
          message: "Dinner is ready"
```

## Limitations

- Air-Mixer does not yet control device volume, so the media players have
  no volume control; set an announcement's `volume` instead.
- Outputs are not sample-synchronised yet (see the roadmap), so the same
  announcement in two rooms can be slightly out of step.
- Only MP3 and WAV are decoded.
- State is polled every five seconds.

## Development

The integration's tests use
[pytest-homeassistant-custom-component](https://github.com/MatthewFlamm/pytest-homeassistant-custom-component):

```sh
python3 -m venv .venv
.venv/bin/pip install -r tests/hass/requirements.txt
.venv/bin/pytest tests/hass
```
