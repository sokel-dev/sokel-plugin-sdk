"""Event sources: validating the declaration, the payload shape, and per-credential reconciliation."""

import asyncio

import pytest
from conftest import SIMPLE_CONTRACT

from sokel import CredEntry, Plugin, SourceCtx, StateBoard
from sokel.events import SourceSupervisor, desired_source_creds


def make_ctx(sent: list, **kw) -> SourceCtx:
    async def publish(subject: str, data: bytes) -> None:
        sent.append((subject, data))

    return SourceCtx(token="skp_x", publish=publish, valid_events=["ping"], **kw)


async def test_trigger_shape():
    sent: list = []
    ctx = make_ctx(sent, credential_id="cred_1")
    await ctx.trigger("ping", "evt-1", {"at": "now"})
    import json

    subject, data = sent[0]
    assert subject == "sokel.trigger"
    assert json.loads(data) == {
        "token": "skp_x",
        "event": "ping",
        "payload": {"at": "now"},
        "event_id": "evt-1",
        "credential_id": "cred_1",  # the SDK carries the routing key back; source code never handles it
    }


async def test_trigger_rejects_undeclared_event():
    """A misspelled event name fails on the spot; otherwise it becomes a message nobody on the platform side
    claims, with no symptom at all."""
    ctx = make_ctx([])
    with pytest.raises(ValueError):
        await ctx.trigger("pong", "1", {})


async def test_update_credential_requires_a_bound_credential():
    ctx = make_ctx([])
    with pytest.raises(RuntimeError):
        await ctx.update_credential({"session": "x"})


async def test_update_credential_publishes_patch():
    sent: list = []
    ctx = make_ctx(sent, credential_id="cred_1", credential={"session": "old"})
    await ctx.update_credential({"session": "new"})
    import json

    subject, data = sent[0]
    assert subject == "sokel.credential.update"
    assert json.loads(data)["patch"] == {"session": "new"}
    assert ctx.credential["session"] == "new"  # the local copy follows too, or the next tick uses the old value


def test_supervisor_reconciles_by_credential():
    started, stopped = [], []

    def start(c: CredEntry):
        started.append((c.id, c.sig()))
        return lambda: stopped.append(c.id)

    s = SourceSupervisor(start)
    s.reconcile([CredEntry("a", {"t": "1"}), CredEntry("b", {"t": "2"})])
    assert sorted(x[0] for x in started) == ["a", "b"]

    # Changed fields mean stop then start: after a session refresh it has to reconnect with the new value
    started.clear()
    s.reconcile([CredEntry("a", {"t": "9"}), CredEntry("b", {"t": "2"})])
    assert stopped == ["a"] and [x[0] for x in started] == ["a"]

    # A shard moving away or a deleted credential stops it
    stopped.clear()
    s.reconcile([CredEntry("a", {"t": "9"})])
    assert stopped == ["b"]


def test_no_credentials_still_runs_one_instance():
    """A plugin without credentials runs one empty-credential instance, on the same code path as with one."""
    assert [c.id for c in desired_source_creds([])] == [""]


def test_state_board_snapshot_is_stable_and_drops_stopped_creds():
    board = StateBoard(now=lambda: "T")
    board.set("src", "b", "running")
    board.set("src", "a", "error", "boom")
    snap = board.snapshot()
    assert [s["credential_id"] for s in snap] == ["a", "b"]
    assert snap[0]["error"] == "boom"
    board.remove_cred("a")
    assert [s["credential_id"] for s in board.snapshot()] == ["b"]


async def test_source_states_ride_along_with_registration():
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")
    p.register_source("poller", "Poller", lambda ctx: asyncio.sleep(0))
    p.board.set("poller", "cred_1", "auth_required", "the session expired")
    states = p.register_payload("i", "h", "T")["source_states"]
    assert states == [
        {"source_id": "poller", "status": "auth_required", "since": states[0]["since"],
         "credential_id": "cred_1", "error": "the session expired"}
    ]
