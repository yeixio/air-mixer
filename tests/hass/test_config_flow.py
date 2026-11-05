"""Tests for the Air-Mixer config flow."""

from homeassistant.config_entries import SOURCE_USER
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import HomeAssistant
from homeassistant.data_entry_flow import FlowResultType
from pytest_homeassistant_custom_component.test_util.aiohttp import (
    AiohttpClientMocker,
)

from custom_components.airmixer.const import DOMAIN

from .conftest import BASE


async def test_user_flow(hass: HomeAssistant, server: AiohttpClientMocker) -> None:
    """A reachable server creates an entry."""
    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": SOURCE_USER}
    )
    assert result["type"] is FlowResultType.FORM
    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: " airmixer.local ", CONF_PORT: 9001}
    )
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["data"] == {CONF_HOST: "airmixer.local", CONF_PORT: 9001}
    assert result["result"].unique_id == "airmixer.local:9001"

    # The same server again is refused.
    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": SOURCE_USER}
    )
    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: "AirMixer.local", CONF_PORT: 9001}
    )
    assert result["type"] is FlowResultType.ABORT
    assert result["reason"] == "already_configured"


async def test_cannot_connect(
    hass: HomeAssistant, aioclient_mock: AiohttpClientMocker
) -> None:
    """An unreachable server shows an error, and retrying works."""
    aioclient_mock.get(f"{BASE}/health", exc=TimeoutError())
    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": SOURCE_USER}
    )
    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: "airmixer.local", CONF_PORT: 9001}
    )
    assert result["type"] is FlowResultType.FORM
    assert result["errors"] == {"base": "cannot_connect"}

    aioclient_mock.clear_requests()
    aioclient_mock.get(f"{BASE}/health", json={"status": "ok"})
    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: "airmixer.local", CONF_PORT: 9001}
    )
    assert result["type"] is FlowResultType.CREATE_ENTRY


async def test_unhealthy(
    hass: HomeAssistant, aioclient_mock: AiohttpClientMocker
) -> None:
    """A server that reports a problem shows an error."""
    aioclient_mock.get(
        f"{BASE}/health", status=503, json={"status": "unhealthy", "error": "db"}
    )
    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": SOURCE_USER}
    )
    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: "airmixer.local", CONF_PORT: 9001}
    )
    assert result["errors"] == {"base": "unknown"}
