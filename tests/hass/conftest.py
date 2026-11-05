"""Fixtures for the Air-Mixer integration tests."""

from __future__ import annotations

from typing import Any

import pytest
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import HomeAssistant
from homeassistant.core_config import async_process_ha_core_config
from homeassistant.setup import async_setup_component
from pytest_homeassistant_custom_component.common import MockConfigEntry
from pytest_homeassistant_custom_component.test_util.aiohttp import (
    AiohttpClientMocker,
)

from custom_components.airmixer.const import DOMAIN

BASE = "http://airmixer.local:9001"

CLIENTS = [
    {
        "id": "client-k",
        "name": "Kitchen",
        "protocol": "airplay",
        "address": "10.0.0.5:5000",
        "enabled": True,
    },
    {
        "id": "client-d",
        "name": "Den",
        "protocol": "local",
        "address": "",
        "enabled": True,
    },
    {
        "id": "client-g",
        "name": "Garage",
        "protocol": "airplay",
        "address": "10.0.0.9:5000",
        "enabled": False,
    },
]

STATUS = {
    "router_running": True,
    "routes_active": 1,
    "version": "1.2.3",
    "adapters": [
        {
            "id": "client-k",
            "name": "Kitchen",
            "protocol": "airplay",
            "kind": "client",
            "state": "connected",
        },
        {
            "id": "client-d",
            "name": "Den",
            "protocol": "local",
            "kind": "client",
            "state": "connecting",
            "error": "no sound card",
        },
    ],
}


@pytest.fixture(autouse=True)
def auto_enable_custom_integrations(enable_custom_integrations: None) -> None:
    """Load custom_components/airmixer."""


@pytest.fixture
def playback() -> list[dict[str, Any]]:
    """The playbacks the mock server reports; tests may append to it."""
    return []


@pytest.fixture
def server(
    aioclient_mock: AiohttpClientMocker, playback: list[dict[str, Any]]
) -> AiohttpClientMocker:
    """Mock an Air-Mixer server."""
    aioclient_mock.get(f"{BASE}/health", json={"status": "ok", "version": "1.2.3"})
    aioclient_mock.get(f"{BASE}/api/clients", json=CLIENTS)
    aioclient_mock.get(f"{BASE}/api/status", json=STATUS)

    async def list_playback(method, url, data):
        from pytest_homeassistant_custom_component.test_util.aiohttp import (
            AiohttpClientMockResponse,
        )

        return AiohttpClientMockResponse(method, url, json={"playback": playback})

    aioclient_mock.get(f"{BASE}/api/playback", side_effect=list_playback)
    aioclient_mock.post(
        f"{BASE}/api/play",
        status=202,
        json={"id": "play-1", "state": "playing", "outputs": ["Kitchen"]},
    )
    aioclient_mock.delete(f"{BASE}/api/playback", json={"stopped": 1})
    return aioclient_mock


@pytest.fixture
def entry() -> MockConfigEntry:
    """A config entry for the mock server."""
    return MockConfigEntry(
        domain=DOMAIN,
        title="Air-Mixer (airmixer.local)",
        unique_id="airmixer.local:9001",
        data={CONF_HOST: "airmixer.local", CONF_PORT: 9001},
    )


@pytest.fixture
async def setup(
    hass: HomeAssistant, server: AiohttpClientMocker, entry: MockConfigEntry
) -> MockConfigEntry:
    """Set up the integration against the mock server."""
    await async_process_ha_core_config(hass, {"internal_url": "http://ha.local:8123"})
    assert await async_setup_component(hass, "media_source", {})
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()
    return entry


def calls(server: AiohttpClientMocker, method: str, path: str) -> list[tuple]:
    """Return the requests made to a path."""
    return [
        c for c in server.mock_calls if c[0].lower() == method and c[1].path == path
    ]
