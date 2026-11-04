# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""Explicit broker network selection, shared by discovery and connection setup."""

import re
from typing import Any, Dict
from urllib.parse import urlsplit

from nats.aio.client import Client

from . import env


def broker_network() -> str:
    network = env.get("BROKER_NETWORK")
    if network in ("", "public"):
        return "public"
    if network == "internal":
        return "internal"
    raise ValueError('invalid SOKEL_BROKER_NETWORK: expected "public" or "internal"')


def internal_broker_url(target: Any) -> str:
    """Never replace a missing or malformed internal address with the public address."""
    message = (
        "internal broker requires nats.internal_url with one valid nats:// or tls:// host "
        "(no credentials, path, query, fragment, or server list)"
    )
    if not isinstance(target, str) or not re.match(r"^(nats|tls)://", target) or re.search(r"[\s\x00-\x1f\x7f,?#\\]", target):
        raise ValueError(message)
    try:
        url = urlsplit(target)
        if not url.hostname or url.username is not None or url.password is not None or url.path or target.endswith(":"):
            raise ValueError(message)
        if url.port is not None and not 1 <= url.port <= 65535:
            raise ValueError(message)
    except ValueError:
        raise ValueError(message) from None
    return target


class InternalBrokerClient(Client):
    """Pin the reconnect pool to the configured internal address.

    nats-py has no public option to ignore discovered servers. This narrow adapter uses its
    _process_info hook for both the initial handshake and asynchronous INFO updates. Keep the
    real-client INFO/reconnect tests when upgrading nats-py; other INFO fields go to the parent.
    """

    def _process_info(self, info: Dict[str, Any], initial_connection: bool = False) -> Any:
        filtered = {key: value for key, value in info.items() if key != "connect_urls"}
        # Older supported versions call this synchronously; newer versions await the result.
        return super()._process_info(filtered, initial_connection=initial_connection)
