"""事件源：声明校验、payload 形态、per-credential reconcile。"""

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
        "credential_id": "cred_1",  # 路由键由 SDK 自动回带，源代码不用管
    }


async def test_trigger_rejects_undeclared_event():
    """拼错的事件名当场报错——否则它变成一条平台侧无人认领的消息，没有任何症状。"""
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
    assert ctx.credential["session"] == "new"  # 本地也要跟上，否则下一拍还用旧值


def test_supervisor_reconciles_by_credential():
    started, stopped = [], []

    def start(c: CredEntry):
        started.append((c.id, c.sig()))
        return lambda: stopped.append(c.id)

    s = SourceSupervisor(start)
    s.reconcile([CredEntry("a", {"t": "1"}), CredEntry("b", {"t": "2"})])
    assert sorted(x[0] for x in started) == ["a", "b"]

    # 字段变了 = 先停后起（会话刷新后必须用新值重连）
    started.clear()
    s.reconcile([CredEntry("a", {"t": "9"}), CredEntry("b", {"t": "2"})])
    assert stopped == ["a"] and [x[0] for x in started] == ["a"]

    # 分片迁走 / 凭证被删 → 停掉
    stopped.clear()
    s.reconcile([CredEntry("a", {"t": "9"})])
    assert stopped == ["b"]


def test_no_credentials_still_runs_one_instance():
    """无凭证插件跑一个空凭证实例，与有凭证时同一条代码路径。"""
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
    p.register_source("poller", "轮询", lambda ctx: asyncio.sleep(0))
    p.board.set("poller", "cred_1", "auth_required", "session 失效")
    states = p.register_payload("i", "h", "T")["source_states"]
    assert states == [
        {"source_id": "poller", "status": "auth_required", "since": states[0]["since"],
         "credential_id": "cred_1", "error": "session 失效"}
    ]
