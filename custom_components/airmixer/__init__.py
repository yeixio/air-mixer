"""The Air-Mixer integration: send audio, speech and alarms to Air-Mixer outputs."""

from __future__ import annotations

from collections import defaultdict
from typing import Any

import voluptuous as vol
from homeassistant.components import media_source
from homeassistant.components.media_player import async_process_play_media_url
from homeassistant.const import ATTR_ENTITY_ID, CONF_HOST, CONF_PORT, Platform
from homeassistant.core import (
    HomeAssistant,
    ServiceCall,
    ServiceResponse,
    SupportsResponse,
)
from homeassistant.exceptions import HomeAssistantError, ServiceValidationError
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers import entity_registry as er
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.typing import ConfigType

from .api import AirMixerClient, AirMixerError
from .const import (
    ATTR_DUCK_LEVEL,
    ATTR_LOOP,
    ATTR_MAX_DURATION,
    ATTR_MODE,
    ATTR_REPEAT,
    ATTR_TITLE,
    ATTR_VOLUME,
    ATTR_WAIT,
    DOMAIN,
    MODE_DUCK,
    MODES,
    SERVICE_ANNOUNCE,
    SERVICE_STOP,
)
from .coordinator import AirMixerConfigEntry, AirMixerCoordinator

PLATFORMS = [Platform.MEDIA_PLAYER]
CONFIG_SCHEMA = cv.config_entry_only_config_schema(DOMAIN)

ATTR_MEDIA = "media_content_id"


def _media_id(value: Any) -> str:
    """Accept a URL or media source ID, or what the media selector returns."""
    if isinstance(value, dict):
        value = value.get("media_content_id")
    return cv.string(value)


ANNOUNCE_SCHEMA = vol.Schema(
    {
        vol.Optional(ATTR_ENTITY_ID): cv.entity_ids,
        vol.Required(ATTR_MEDIA): _media_id,
        vol.Optional(ATTR_MODE, default=MODE_DUCK): vol.In(MODES),
        vol.Optional(ATTR_VOLUME): vol.All(vol.Coerce(float), vol.Range(0, 1)),
        vol.Optional(ATTR_DUCK_LEVEL): vol.All(vol.Coerce(float), vol.Range(0, 1)),
        vol.Optional(ATTR_REPEAT): vol.All(vol.Coerce(int), vol.Range(1, 100)),
        vol.Optional(ATTR_LOOP): cv.boolean,
        vol.Optional(ATTR_MAX_DURATION): vol.All(vol.Coerce(float), vol.Range(min=0)),
        vol.Optional(ATTR_TITLE): cv.string,
        vol.Optional(ATTR_WAIT, default=False): cv.boolean,
    }
)
STOP_SCHEMA = vol.Schema({vol.Optional(ATTR_ENTITY_ID): cv.entity_ids})


async def async_setup(hass: HomeAssistant, config: ConfigType) -> bool:
    """Register the airmixer.announce and airmixer.stop actions."""

    async def announce(call: ServiceCall) -> ServiceResponse:
        targets = _targets(hass, call.data.get(ATTR_ENTITY_ID))
        url = await resolve_media_url(hass, call.data[ATTR_MEDIA])
        request: dict[str, Any] = {"url": url, ATTR_MODE: call.data[ATTR_MODE]}
        for key in (
            ATTR_VOLUME,
            ATTR_DUCK_LEVEL,
            ATTR_REPEAT,
            ATTR_LOOP,
            ATTR_MAX_DURATION,
            ATTR_TITLE,
        ):
            if key in call.data:
                request[key] = call.data[key]
        started = []
        for entry, outputs in targets.items():
            try:
                item = await entry.runtime_data.client.play(
                    {**request, "outputs": outputs}, wait=call.data[ATTR_WAIT]
                )
            except AirMixerError as err:
                raise HomeAssistantError(f"Air-Mixer could not play: {err}") from err
            started.append(item)
        for entry in targets:
            await entry.runtime_data.async_request_refresh()
        return {"playback": started}

    async def stop(call: ServiceCall) -> None:
        targets = _targets(hass, call.data.get(ATTR_ENTITY_ID))
        for entry, outputs in targets.items():
            client = entry.runtime_data.client
            try:
                if outputs:
                    for output in outputs:
                        await client.stop_output(output)
                else:
                    await client.stop_all()
            except AirMixerError as err:
                raise HomeAssistantError(f"Air-Mixer could not stop: {err}") from err
            await entry.runtime_data.async_request_refresh()

    hass.services.async_register(
        DOMAIN,
        SERVICE_ANNOUNCE,
        announce,
        schema=ANNOUNCE_SCHEMA,
        supports_response=SupportsResponse.OPTIONAL,
    )
    hass.services.async_register(DOMAIN, SERVICE_STOP, stop, schema=STOP_SCHEMA)
    return True


def _targets(
    hass: HomeAssistant, entity_ids: list[str] | None
) -> dict[AirMixerConfigEntry, list[str]]:
    """Group target entities by Air-Mixer server.

    The value lists output IDs, or is empty for every output. No entity
    IDs means every output of every server.
    """
    entries: list[AirMixerConfigEntry] = hass.config_entries.async_loaded_entries(
        DOMAIN
    )
    if not entries:
        raise ServiceValidationError("No Air-Mixer server is set up")
    if not entity_ids:
        return {entry: [] for entry in entries}

    by_id = {entry.entry_id: entry for entry in entries}
    registry = er.async_get(hass)
    targets: dict[AirMixerConfigEntry, list[str]] = defaultdict(list)
    whole: set[str] = set()
    for entity_id in entity_ids:
        reg = registry.async_get(entity_id)
        if reg is None or reg.platform != DOMAIN or reg.config_entry_id not in by_id:
            raise ServiceValidationError(f"{entity_id} is not an Air-Mixer output")
        entry = by_id[reg.config_entry_id]
        output = reg.unique_id.removeprefix(f"{entry.entry_id}_")
        if output == "all":
            whole.add(entry.entry_id)
        targets[entry].append(output)
    # An "all outputs" target covers the server's other targets.
    return {e: ([] if e.entry_id in whole else o) for e, o in targets.items()}


async def resolve_media_url(hass: HomeAssistant, media_id: str) -> str:
    """Turn a media source ID or relative URL into a URL Air-Mixer can fetch."""
    if media_source.is_media_source_id(media_id):
        try:
            item = await media_source.async_resolve_media(hass, media_id, None)
        except media_source.Unresolvable as err:
            raise ServiceValidationError(f"Cannot play {media_id}: {err}") from err
        media_id = item.url
    return async_process_play_media_url(hass, media_id)


async def async_setup_entry(hass: HomeAssistant, entry: AirMixerConfigEntry) -> bool:
    """Set up Air-Mixer from a config entry."""
    client = AirMixerClient(
        async_get_clientsession(hass), entry.data[CONF_HOST], entry.data[CONF_PORT]
    )
    coordinator = AirMixerCoordinator(hass, entry, client)
    await coordinator.async_config_entry_first_refresh()
    entry.runtime_data = coordinator
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: AirMixerConfigEntry) -> bool:
    """Unload a config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
