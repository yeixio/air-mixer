"""Config flow for Air-Mixer."""

from __future__ import annotations

from typing import Any

import voluptuous as vol
from homeassistant.config_entries import ConfigFlow, ConfigFlowResult
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import AirMixerClient, AirMixerConnectionError, AirMixerError
from .const import DEFAULT_PORT, DOMAIN

STEP_USER_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_HOST): str,
        vol.Required(CONF_PORT, default=DEFAULT_PORT): vol.All(
            vol.Coerce(int), vol.Range(min=1, max=65535)
        ),
    }
)


class AirMixerConfigFlow(ConfigFlow, domain=DOMAIN):
    """Ask for the Air-Mixer server's address and check it answers."""

    VERSION = 1

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle the user step."""
        errors: dict[str, str] = {}
        if user_input is not None:
            host = user_input[CONF_HOST].strip()
            port = user_input[CONF_PORT]
            await self.async_set_unique_id(f"{host.lower()}:{port}")
            self._abort_if_unique_id_configured()
            client = AirMixerClient(async_get_clientsession(self.hass), host, port)
            try:
                health = await client.health()
            except AirMixerConnectionError:
                errors["base"] = "cannot_connect"
            except AirMixerError:
                errors["base"] = "unknown"
            else:
                if health.get("status") != "ok":
                    errors["base"] = "unhealthy"
                else:
                    return self.async_create_entry(
                        title=f"Air-Mixer ({host})",
                        data={CONF_HOST: host, CONF_PORT: port},
                    )
        return self.async_show_form(
            step_id="user",
            data_schema=self.add_suggested_values_to_schema(
                STEP_USER_SCHEMA, user_input
            ),
            errors=errors,
        )
