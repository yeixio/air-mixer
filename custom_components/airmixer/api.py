"""A small async client for the Air-Mixer REST API (see docs/API.md)."""

from __future__ import annotations

from typing import Any

import aiohttp


class AirMixerError(Exception):
    """Air-Mixer returned an error or could not be reached."""


class AirMixerConnectionError(AirMixerError):
    """Air-Mixer could not be reached."""


class AirMixerClient:
    """Talks to one Air-Mixer server."""

    def __init__(self, session: aiohttp.ClientSession, host: str, port: int) -> None:
        """Initialize the client."""
        self._session = session
        self.base_url = f"http://{host}:{port}"

    async def _request(
        self,
        method: str,
        path: str,
        *,
        json: dict[str, Any] | None = None,
        params: dict[str, str] | None = None,
        timeout: float = 10,
    ) -> Any:
        try:
            async with self._session.request(
                method,
                self.base_url + path,
                json=json,
                params=params,
                timeout=aiohttp.ClientTimeout(total=timeout),
            ) as resp:
                if resp.status == 204:
                    return None
                try:
                    body = await resp.json(content_type=None)
                except ValueError:
                    body = None
                if resp.status >= 400:
                    message = body.get("error") if isinstance(body, dict) else None
                    raise AirMixerError(message or f"HTTP {resp.status}")
                return body
        except (aiohttp.ClientError, TimeoutError) as err:
            raise AirMixerConnectionError(
                f"cannot reach Air-Mixer at {self.base_url}: {err}"
            ) from err

    async def health(self) -> dict[str, Any]:
        """Return {"status": "ok", "version": ...}."""
        return await self._request("GET", "/health")

    async def clients(self) -> list[dict[str, Any]]:
        """Return the configured outputs."""
        return await self._request("GET", "/api/clients") or []

    async def status(self) -> dict[str, Any]:
        """Return the router and adapter status."""
        return await self._request("GET", "/api/status") or {}

    async def playback(self) -> list[dict[str, Any]]:
        """Return the playbacks in progress."""
        body = await self._request("GET", "/api/playback") or {}
        return body.get("playback") or []

    async def play(
        self, request: dict[str, Any], *, wait: bool = False
    ) -> dict[str, Any]:
        """Start a playback. With wait, return when it ends."""
        payload = {**request, "wait": wait}
        # A playback may last up to an hour; when waiting, let it.
        return await self._request(
            "POST", "/api/play", json=payload, timeout=3600 if wait else 30
        )

    async def stop(self, playback_id: str) -> None:
        """Stop one playback."""
        await self._request("DELETE", f"/api/playback/{playback_id}")

    async def stop_output(self, output: str) -> int:
        """Stop playback on one output, by ID or name."""
        body = await self._request("DELETE", "/api/playback", params={"output": output})
        return int((body or {}).get("stopped", 0))

    async def stop_all(self) -> int:
        """Stop every playback."""
        body = await self._request("DELETE", "/api/playback")
        return int((body or {}).get("stopped", 0))
