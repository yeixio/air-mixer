"""Tests for the Air-Mixer media players and actions."""

from typing import Any
from unittest.mock import patch

import pytest
from homeassistant.components.media_player import (
    ATTR_MEDIA_ANNOUNCE,
    ATTR_MEDIA_CONTENT_ID,
    ATTR_MEDIA_CONTENT_TYPE,
    ATTR_MEDIA_EXTRA,
    ATTR_MEDIA_TITLE,
    SERVICE_PLAY_MEDIA,
    MediaPlayerEntityFeature,
)
from homeassistant.components.media_player import (
    DOMAIN as MP_DOMAIN,
)
from homeassistant.components.media_source import PlayMedia
from homeassistant.config_entries import ConfigEntryState
from homeassistant.const import (
    ATTR_ENTITY_ID,
    ATTR_SUPPORTED_FEATURES,
    SERVICE_MEDIA_STOP,
)
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError, ServiceValidationError
from homeassistant.util import dt as dt_util
from pytest_homeassistant_custom_component.common import (
    MockConfigEntry,
    async_fire_time_changed,
)
from pytest_homeassistant_custom_component.test_util.aiohttp import (
    AiohttpClientMocker,
)

from custom_components.airmixer.const import DOMAIN, SCAN_INTERVAL

from .conftest import BASE, calls

KITCHEN = "media_player.kitchen"
DEN = "media_player.den"
GARAGE = "media_player.garage"
ALL = "media_player.air_mixer_all_outputs"


async def test_entities(hass: HomeAssistant, setup: MockConfigEntry) -> None:
    """Every output and the all-outputs player become entities."""
    assert setup.state is ConfigEntryState.LOADED
    kitchen = hass.states.get(KITCHEN)
    assert kitchen.state == "idle"
    assert kitchen.attributes["connection"] == "connected"
    assert kitchen.attributes["protocol"] == "airplay"
    features = kitchen.attributes[ATTR_SUPPORTED_FEATURES]
    assert features & MediaPlayerEntityFeature.MEDIA_ANNOUNCE
    assert features & MediaPlayerEntityFeature.PLAY_MEDIA
    assert hass.states.get(DEN).attributes["connection_error"] == "no sound card"
    assert hass.states.get(GARAGE).state == "off"
    assert hass.states.get(ALL).state == "idle"


async def test_state_follows_playback(
    hass: HomeAssistant, setup: MockConfigEntry, playback: list[dict[str, Any]]
) -> None:
    """An output plays while a playback includes it."""
    playback.append(
        {
            "id": "play-9",
            "url": "http://x/a.mp3",
            "title": "Doorbell",
            "outputs": ["Kitchen"],
            "position": 1.5,
        }
    )
    async_fire_time_changed(hass, dt_util.utcnow() + SCAN_INTERVAL)
    await hass.async_block_till_done()
    kitchen = hass.states.get(KITCHEN)
    assert kitchen.state == "playing"
    assert kitchen.attributes[ATTR_MEDIA_TITLE] == "Doorbell"
    assert hass.states.get(DEN).state == "idle"
    assert hass.states.get(ALL).state == "playing"


async def test_tts_is_an_announcement(
    hass: HomeAssistant, setup: MockConfigEntry, server: AiohttpClientMocker
) -> None:
    """Text-to-speech ducks by default and gets an absolute URL."""
    with patch(
        "custom_components.airmixer.media_source.async_resolve_media",
        return_value=PlayMedia(url="/api/tts_proxy/abc.mp3", mime_type="audio/mpeg"),
    ):
        await hass.services.async_call(
            MP_DOMAIN,
            SERVICE_PLAY_MEDIA,
            {
                ATTR_ENTITY_ID: KITCHEN,
                ATTR_MEDIA_CONTENT_ID: "media-source://tts/cloud?message=hello",
                ATTR_MEDIA_CONTENT_TYPE: "music",
            },
            blocking=True,
        )
    (call,) = calls(server, "post", "/api/play")
    body = call[2]
    assert body["outputs"] == ["client-k"]
    assert body["mode"] == "duck"
    assert body["title"] == "Announcement"
    assert body["url"].startswith("http://ha.local:8123/api/tts_proxy/abc.mp3")


async def test_play_url_and_extra(
    hass: HomeAssistant, setup: MockConfigEntry, server: AiohttpClientMocker
) -> None:
    """A plain URL mixes unless announced, and extra sets options."""
    await hass.services.async_call(
        MP_DOMAIN,
        SERVICE_PLAY_MEDIA,
        {
            ATTR_ENTITY_ID: DEN,
            ATTR_MEDIA_CONTENT_ID: "http://radio.example/stream.mp3",
            ATTR_MEDIA_CONTENT_TYPE: "music",
        },
        blocking=True,
    )
    await hass.services.async_call(
        MP_DOMAIN,
        SERVICE_PLAY_MEDIA,
        {
            ATTR_ENTITY_ID: ALL,
            ATTR_MEDIA_CONTENT_ID: "http://ha.local/alarm.wav",
            ATTR_MEDIA_CONTENT_TYPE: "music",
            ATTR_MEDIA_ANNOUNCE: True,
            ATTR_MEDIA_EXTRA: {
                "mode": "interrupt",
                "loop": True,
                "volume": 1,
                "ignored": 1,
            },
        },
        blocking=True,
    )
    first, second = (c[2] for c in calls(server, "post", "/api/play"))
    assert first == {
        "url": "http://radio.example/stream.mp3",
        "outputs": ["client-d"],
        "mode": "mix",
        "wait": False,
    }
    assert second == {
        "url": "http://ha.local/alarm.wav",
        "outputs": [],
        "mode": "interrupt",
        "loop": True,
        "volume": 1,
        "wait": False,
    }


async def test_play_error(
    hass: HomeAssistant, setup: MockConfigEntry, server: AiohttpClientMocker
) -> None:
    """Air-Mixer's error message reaches the user."""
    server.clear_requests()
    server.post(
        f"{BASE}/api/play", status=502, json={"error": "unsupported audio format: Ogg"}
    )
    with pytest.raises(HomeAssistantError, match="unsupported audio format"):
        await hass.services.async_call(
            MP_DOMAIN,
            SERVICE_PLAY_MEDIA,
            {
                ATTR_ENTITY_ID: KITCHEN,
                ATTR_MEDIA_CONTENT_ID: "http://x/a.ogg",
                ATTR_MEDIA_CONTENT_TYPE: "music",
            },
            blocking=True,
        )


async def test_media_stop(
    hass: HomeAssistant, setup: MockConfigEntry, server: AiohttpClientMocker
) -> None:
    """Stopping an output stops only its playback; all outputs stops all."""
    await hass.services.async_call(
        MP_DOMAIN, SERVICE_MEDIA_STOP, {ATTR_ENTITY_ID: KITCHEN}, blocking=True
    )
    await hass.services.async_call(
        MP_DOMAIN, SERVICE_MEDIA_STOP, {ATTR_ENTITY_ID: ALL}, blocking=True
    )
    one, every = calls(server, "delete", "/api/playback")
    assert one[1].query == {"output": "client-k"}
    assert dict(every[1].query) == {}


async def test_announce_action(
    hass: HomeAssistant, setup: MockConfigEntry, server: AiohttpClientMocker
) -> None:
    """One announce action sends one request for all its outputs."""
    response = await hass.services.async_call(
        DOMAIN,
        "announce",
        {
            ATTR_ENTITY_ID: [KITCHEN, DEN],
            "media_content_id": {
                "media_content_id": "http://ha.local/doorbell.mp3",
                "media_content_type": "audio/mpeg",
            },
            "duck_level": 0.1,
            "repeat": 2,
            "title": "Doorbell",
        },
        blocking=True,
        return_response=True,
    )
    assert response == {
        "playback": [{"id": "play-1", "state": "playing", "outputs": ["Kitchen"]}]
    }
    (call,) = calls(server, "post", "/api/play")
    assert call[2] == {
        "url": "http://ha.local/doorbell.mp3",
        "outputs": ["client-k", "client-d"],
        "mode": "duck",
        "duck_level": 0.1,
        "repeat": 2,
        "title": "Doorbell",
        "wait": False,
    }

    # No targets, or the all-outputs player, means every output.
    for target in ({}, {ATTR_ENTITY_ID: [ALL, KITCHEN]}):
        await hass.services.async_call(
            DOMAIN,
            "announce",
            {**target, "media_content_id": "http://ha.local/a.mp3"},
            blocking=True,
        )
    assert [c[2]["outputs"] for c in calls(server, "post", "/api/play")][1:] == [[], []]


async def test_announce_rejects_other_entities(
    hass: HomeAssistant, setup: MockConfigEntry
) -> None:
    """Only Air-Mixer outputs can be targeted."""
    with pytest.raises(ServiceValidationError):
        await hass.services.async_call(
            DOMAIN,
            "announce",
            {
                ATTR_ENTITY_ID: ["media_player.elsewhere"],
                "media_content_id": "http://x/a.mp3",
            },
            blocking=True,
        )


async def test_stop_action(
    hass: HomeAssistant, setup: MockConfigEntry, server: AiohttpClientMocker
) -> None:
    """The stop action stops named outputs, or everything."""
    await hass.services.async_call(
        DOMAIN, "stop", {ATTR_ENTITY_ID: [DEN]}, blocking=True
    )
    await hass.services.async_call(DOMAIN, "stop", {}, blocking=True)
    named, every = calls(server, "delete", "/api/playback")
    assert named[1].query == {"output": "client-d"}
    assert dict(every[1].query) == {}


async def test_unload_and_unavailable(
    hass: HomeAssistant, setup: MockConfigEntry, server: AiohttpClientMocker
) -> None:
    """Entities go unavailable when the server stops answering."""
    server.clear_requests()
    server.get(f"{BASE}/api/clients", exc=TimeoutError())
    async_fire_time_changed(hass, dt_util.utcnow() + SCAN_INTERVAL)
    await hass.async_block_till_done()
    assert hass.states.get(KITCHEN).state == "unavailable"

    assert await hass.config_entries.async_unload(setup.entry_id)
    assert setup.state is ConfigEntryState.NOT_LOADED
