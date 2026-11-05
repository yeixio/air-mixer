"""Polls Air-Mixer for outputs, their state and playback."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from homeassistant.util import dt as dt_util

from .api import AirMixerClient, AirMixerError
from .const import DOMAIN, SCAN_INTERVAL

_LOGGER = logging.getLogger(__name__)

type AirMixerConfigEntry = ConfigEntry[AirMixerCoordinator]


@dataclass
class AirMixerData:
    """A snapshot of the server."""

    version: str | None = None
    # Outputs by ID, as returned by GET /api/clients.
    outputs: dict[str, dict[str, Any]] = field(default_factory=dict)
    # Adapter status by ID, from GET /api/status.
    adapters: dict[str, dict[str, Any]] = field(default_factory=dict)
    # Playbacks in progress.
    playback: list[dict[str, Any]] = field(default_factory=list)
    # When the snapshot was taken.
    fetched_at: datetime | None = None

    def playback_on(self, output_name: str) -> list[dict[str, Any]]:
        """Return the playbacks that include an output."""
        return [p for p in self.playback if output_name in (p.get("outputs") or [])]


class AirMixerCoordinator(DataUpdateCoordinator[AirMixerData]):
    """Keeps an AirMixerData up to date."""

    config_entry: AirMixerConfigEntry

    def __init__(
        self, hass: HomeAssistant, entry: AirMixerConfigEntry, client: AirMixerClient
    ) -> None:
        """Initialize the coordinator."""
        super().__init__(
            hass,
            _LOGGER,
            config_entry=entry,
            name=DOMAIN,
            update_interval=SCAN_INTERVAL,
        )
        self.client = client

    async def _async_update_data(self) -> AirMixerData:
        try:
            clients = await self.client.clients()
            status = await self.client.status()
            playback = await self.client.playback()
        except AirMixerError as err:
            raise UpdateFailed(str(err)) from err
        return AirMixerData(
            version=status.get("version"),
            outputs={c["id"]: c for c in clients},
            adapters={a["id"]: a for a in status.get("adapters") or []},
            playback=playback,
            fetched_at=dt_util.utcnow(),
        )
