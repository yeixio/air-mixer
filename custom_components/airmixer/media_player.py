"""Media player entities: one per Air-Mixer output, plus one for all outputs."""

from __future__ import annotations

from datetime import datetime
from typing import Any

from homeassistant.components import media_source
from homeassistant.components.media_player import (
    ATTR_MEDIA_ANNOUNCE,
    ATTR_MEDIA_EXTRA,
    BrowseMedia,
    MediaPlayerEntity,
    MediaPlayerEntityFeature,
    MediaPlayerState,
    MediaType,
)
from homeassistant.core import HomeAssistant, callback
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from . import resolve_media_url
from .api import AirMixerError
from .const import (
    ATTR_DUCK_LEVEL,
    ATTR_LOOP,
    ATTR_MAX_DURATION,
    ATTR_MODE,
    ATTR_REPEAT,
    ATTR_TITLE,
    ATTR_VOLUME,
    DOMAIN,
    MODE_DUCK,
    MODE_MIX,
    MODES,
)
from .coordinator import AirMixerConfigEntry, AirMixerCoordinator

ALL_OUTPUTS = "all"

# Options a play_media call may pass in "extra".
EXTRA_KEYS = (
    ATTR_MODE,
    ATTR_VOLUME,
    ATTR_DUCK_LEVEL,
    ATTR_REPEAT,
    ATTR_LOOP,
    ATTR_MAX_DURATION,
    ATTR_TITLE,
)


async def async_setup_entry(
    hass: HomeAssistant,
    entry: AirMixerConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Add an entity for every output, and for new outputs as they appear."""
    coordinator = entry.runtime_data
    known: set[str] = set()

    @callback
    def add_new() -> None:
        new = [
            AirMixerOutput(coordinator, output_id)
            for output_id in coordinator.data.outputs
            if output_id not in known
        ]
        known.update(e.output_id for e in new)
        if new:
            async_add_entities(new)

    async_add_entities([AirMixerAllOutputs(coordinator)])
    add_new()
    entry.async_on_unload(coordinator.async_add_listener(add_new))


class AirMixerPlayer(CoordinatorEntity[AirMixerCoordinator], MediaPlayerEntity):
    """Common behaviour of output entities."""

    _attr_has_entity_name = True
    _attr_media_content_type = MediaType.MUSIC
    _attr_supported_features = (
        MediaPlayerEntityFeature.PLAY_MEDIA
        | MediaPlayerEntityFeature.MEDIA_ANNOUNCE
        | MediaPlayerEntityFeature.STOP
        | MediaPlayerEntityFeature.BROWSE_MEDIA
    )
    output_id: str

    def __init__(self, coordinator: AirMixerCoordinator, output_id: str) -> None:
        """Initialize the entity."""
        super().__init__(coordinator)
        self.output_id = output_id
        self._attr_unique_id = f"{coordinator.config_entry.entry_id}_{output_id}"

    @property
    def _server_device(self) -> tuple[str, str]:
        return (DOMAIN, self.coordinator.config_entry.entry_id)

    def _playbacks(self) -> list[dict[str, Any]]:
        raise NotImplementedError

    def _outputs(self) -> list[str]:
        """The output names to play on; empty means all."""
        raise NotImplementedError

    @property
    def state(self) -> MediaPlayerState:
        """Playing while Air-Mixer plays something sent here."""
        return MediaPlayerState.PLAYING if self._playbacks() else MediaPlayerState.IDLE

    @property
    def _current(self) -> dict[str, Any] | None:
        playing = self._playbacks()
        return playing[-1] if playing else None

    @property
    def media_title(self) -> str | None:
        """The title of the latest playback."""
        if (current := self._current) is None:
            return None
        return current.get("title") or current.get("url")

    @property
    def media_content_id(self) -> str | None:
        """The URL of the latest playback."""
        current = self._current
        return current.get("url") if current else None

    @property
    def media_position(self) -> float | None:
        """Seconds played of the latest playback."""
        current = self._current
        return current.get("position") if current else None

    @property
    def media_position_updated_at(self) -> datetime | None:
        """When the position was read."""
        return self.coordinator.data.fetched_at if self._current else None

    async def async_play_media(
        self, media_type: MediaType | str, media_id: str, **kwargs: Any
    ) -> None:
        """Play a URL or media source item.

        Announcements, and text-to-speech unless told otherwise, duck other
        audio; anything else plays alongside it. "extra" may set mode,
        volume, duck_level, repeat, loop, max_duration and title.
        """
        is_tts = media_id.startswith("media-source://tts")
        announce = kwargs.get(ATTR_MEDIA_ANNOUNCE)
        if announce is None:
            announce = is_tts
        request: dict[str, Any] = {
            "url": await resolve_media_url(self.hass, media_id),
            "outputs": self._outputs(),
            ATTR_MODE: MODE_DUCK if announce else MODE_MIX,
        }
        extra = kwargs.get(ATTR_MEDIA_EXTRA) or {}
        for key in EXTRA_KEYS:
            if key in extra:
                request[key] = extra[key]
        if request[ATTR_MODE] not in MODES:
            raise HomeAssistantError(f"mode must be one of {', '.join(MODES)}")
        if ATTR_TITLE not in request and is_tts:
            request[ATTR_TITLE] = "Announcement"
        try:
            await self.coordinator.client.play(request)
        except AirMixerError as err:
            raise HomeAssistantError(f"Air-Mixer could not play: {err}") from err
        await self.coordinator.async_request_refresh()

    async def async_browse_media(
        self,
        media_content_type: MediaType | str | None = None,
        media_content_id: str | None = None,
    ) -> BrowseMedia:
        """Browse audio in Home Assistant's media sources."""
        return await media_source.async_browse_media(
            self.hass,
            media_content_id,
            content_filter=lambda item: item.media_content_type.startswith("audio/"),
        )


class AirMixerOutput(AirMixerPlayer):
    """One Air-Mixer output (client)."""

    _attr_name = None  # the device name

    def __init__(self, coordinator: AirMixerCoordinator, output_id: str) -> None:
        """Initialize the entity."""
        super().__init__(coordinator, output_id)
        output = coordinator.data.outputs[output_id]
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, self._attr_unique_id)},
            name=output["name"],
            manufacturer="Air-Mixer",
            model=f"{output.get('protocol', 'unknown')} output",
            via_device=self._server_device,
        )

    @property
    def _output(self) -> dict[str, Any] | None:
        return self.coordinator.data.outputs.get(self.output_id)

    @property
    def available(self) -> bool:
        """Available while the output exists on the server."""
        return super().available and self._output is not None

    @property
    def state(self) -> MediaPlayerState:
        """Off while the output is disabled on the server."""
        output = self._output
        if output is not None and not output.get("enabled", True):
            return MediaPlayerState.OFF
        return super().state

    def _playbacks(self) -> list[dict[str, Any]]:
        output = self._output
        return self.coordinator.data.playback_on(output["name"]) if output else []

    def _outputs(self) -> list[str]:
        return [self.output_id]

    @property
    def extra_state_attributes(self) -> dict[str, Any]:
        """Connection details from the server."""
        adapter = self.coordinator.data.adapters.get(self.output_id) or {}
        output = self._output or {}
        attrs = {
            "protocol": output.get("protocol"),
            "address": output.get("address"),
            "connection": adapter.get("state"),
        }
        if adapter.get("error"):
            attrs["connection_error"] = adapter["error"]
        return attrs

    async def async_media_stop(self) -> None:
        """Stop what Home Assistant sent to this output."""
        try:
            await self.coordinator.client.stop_output(self.output_id)
        except AirMixerError as err:
            raise HomeAssistantError(f"Air-Mixer could not stop: {err}") from err
        await self.coordinator.async_request_refresh()


class AirMixerAllOutputs(AirMixerPlayer):
    """Every enabled output at once, for house-wide announcements."""

    _attr_translation_key = "all_outputs"

    def __init__(self, coordinator: AirMixerCoordinator) -> None:
        """Initialize the entity."""
        super().__init__(coordinator, ALL_OUTPUTS)
        self._attr_device_info = DeviceInfo(
            identifiers={self._server_device},
            name="Air-Mixer",
            manufacturer="Air-Mixer",
            model="Air-Mixer server",
            sw_version=coordinator.data.version,
            configuration_url=coordinator.client.base_url,
        )

    def _playbacks(self) -> list[dict[str, Any]]:
        return self.coordinator.data.playback

    def _outputs(self) -> list[str]:
        return []

    async def async_media_stop(self) -> None:
        """Stop everything Home Assistant sent."""
        try:
            await self.coordinator.client.stop_all()
        except AirMixerError as err:
            raise HomeAssistantError(f"Air-Mixer could not stop: {err}") from err
        await self.coordinator.async_request_refresh()
