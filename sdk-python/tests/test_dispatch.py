"""分发：未知操作、非流式合并、流式逐帧。

这段是最值得单测的一段——它决定平台看到的回复长什么样，而线上出问题时
最难查的恰恰是「回复形态不对」（节点拿到空对象，日志里一切正常）。
"""

import pytest
from conftest import SIMPLE_CONTRACT

from sokel import Ctx, Emitter, Plugin


def make_plugin() -> Plugin:
    return Plugin(SIMPLE_CONTRACT, name="demo", token="skp_test")


async def test_unary_merges_vars_frames():
    p = make_plugin()

    async def greet(ctx: Ctx, raw, out: Emitter) -> None:
        out.vars({"text": f"hi {raw['who']}"})
        out.text("这条只在流式里给人看，不进输出")

    p.register("greet", greet)
    got = await p.dispatch_buffered({"operation": "greet", "input": {"who": "sokel"}})
    assert got == {"text": "hi sokel"}


async def test_unknown_operation_is_an_error():
    p = make_plugin()
    p.register("greet", _noop)
    p.register("stream_it", _noop)
    with pytest.raises(LookupError):
        await p.dispatch_buffered({"operation": "nope", "input": {}})


async def test_single_operation_plugin_defaults_to_it():
    """单操作插件：operation 省略时打到唯一那个（与 Go SDK 同一条兜底）。"""
    p = make_plugin()
    p.register("greet", _echo_who)
    assert await p.dispatch_buffered({"input": {"who": "x"}}) == {"text": "hi x"}


async def test_stream_emits_frames_in_order():
    p = make_plugin()

    async def streamer(ctx: Ctx, raw, out: Emitter) -> None:
        out.text("a")
        out.json({"k": 1})
        out.vars({"n": 2})

    p.register("stream_it", streamer)
    frames = []
    await p.dispatch({"operation": "stream_it", "input": {}}, frames.append)
    assert [f["kind"] for f in frames] == ["text", "json", "variables"]
    assert frames[2]["vars"] == {"n": 2}


async def test_register_rejects_operations_not_in_contract():
    """契约里没有的操作注册不上——否则那份实现永远等不到调用，而且毫无症状。"""
    p = make_plugin()
    with pytest.raises(ValueError):
        p.register("ghost", _noop)


async def test_credential_and_trace_reach_the_handler():
    p = make_plugin()
    seen = {}

    async def greet(ctx: Ctx, raw, out: Emitter) -> None:
        seen["cred"] = ctx.credential
        seen["run"] = ctx.trace("run_id")
        seen["missing"] = ctx.trace("node_id")
        out.vars({"text": "ok"})

    p.register("greet", greet)
    await p.dispatch_buffered(
        {
            "operation": "greet",
            "input": {"who": "x"},
            "credential": {"api_key": "k"},
            "trace": {"run_id": "run_1"},
        }
    )
    assert seen["cred"] == {"api_key": "k"}
    assert seen["run"] == "run_1"
    # 没有追踪上下文时返回空串：调用方要把它当「没有重试语义」，而不是一个恒定的键
    assert seen["missing"] == ""


async def _noop(ctx, raw, out) -> None:
    pass


async def _echo_who(ctx, raw, out) -> None:
    out.vars({"text": f"hi {raw['who']}"})
