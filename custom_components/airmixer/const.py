"""Constants for the Air-Mixer integration."""

from datetime import timedelta
from typing import Final

DOMAIN: Final = "airmixer"
DEFAULT_PORT: Final = 9001
SCAN_INTERVAL: Final = timedelta(seconds=5)

MODE_MIX: Final = "mix"
MODE_DUCK: Final = "duck"
MODE_INTERRUPT: Final = "interrupt"
MODES: Final = [MODE_MIX, MODE_DUCK, MODE_INTERRUPT]

ATTR_MODE: Final = "mode"
ATTR_VOLUME: Final = "volume"
ATTR_DUCK_LEVEL: Final = "duck_level"
ATTR_REPEAT: Final = "repeat"
ATTR_LOOP: Final = "loop"
ATTR_MAX_DURATION: Final = "max_duration"
ATTR_TITLE: Final = "title"
ATTR_WAIT: Final = "wait"

SERVICE_ANNOUNCE: Final = "announce"
SERVICE_STOP: Final = "stop"
