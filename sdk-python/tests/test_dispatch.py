"""Dispatch: an unknown operation, non-streaming merging, and streaming frame by frame.

This is the part most worth unit testing: it decides what the platform sees as a reply, and in production
the hardest fault to track down is exactly "the reply has the wrong shape" — the node receives an empty
object while the logs look fine.
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
        out.text("this is for a human watching the stream and never becomes an output")

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
    """A single-operation plugin: omitting operation hits the only one, the same fallback as the Go SDK."""
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
    """An operation absent from the contract cannot be registered; otherwise the implementation waits forever
    for a call, with no symptom at all."""
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
    # With no trace context it returns an empty string, which callers must read as "no retry semantics"
    # rather than as a constant key
    assert seen["missing"] == ""


async def _noop(ctx, raw, out) -> None:
    pass


async def _echo_who(ctx, raw, out) -> None:
    out.vars({"text": f"hi {raw['who']}"})
