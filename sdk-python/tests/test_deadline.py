"""The handler is cancelled at the platform's deadline_ms (F-327)."""

import asyncio

import pytest

from sokel import Retryable
from sokel.nats_transport import with_deadline


def test_cancelled_at_deadline():
    async def slow():
        await asyncio.sleep(5)

    with pytest.raises(Retryable):
        asyncio.run(with_deadline(slow(), 50))


def test_no_deadline_means_no_limit():
    async def quick():
        return 7

    assert asyncio.run(with_deadline(quick(), None)) == 7
    assert asyncio.run(with_deadline(quick(), 0)) == 7
