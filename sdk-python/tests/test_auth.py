"""Collaborative authentication: the steps follow from the declaration, and the implementation hangs off
reserved operation ids only."""

import pytest

from sokel import AuthChallenge, AuthState, Plugin

CONTRACT = {
    "name": "demo",
    "operations": [{"id": "noop", "inputs": [], "outputs": []}],
    "auth_flow": {"kind": "input", "steps": ["start", "poll", "submit"]},
}


def make_plugin() -> Plugin:
    return Plugin(dict(CONTRACT), name="demo", token="t")


async def test_start_poll_submit_shapes():
    p = make_plugin()
    submitted = {}

    p.register_auth(
        start=lambda ctx: AuthChallenge(auth_id="a1", prompt="Enter the verification code", expires_in=60),
        poll=lambda ctx, aid: AuthState(status="confirmed", session={"k": "v"}) if submitted else AuthState(status="pending"),
        submit=lambda ctx, aid, value: submitted.update({aid: value}),
    )

    start = await p.dispatch_buffered({"operation": "auth.start", "input": {}})
    assert start["auth_id"] == "a1"
    assert start["challenge"] == {"kind": "input", "qr_image": "", "prompt": "Enter the verification code"}
    assert start["expires_in"] == 60

    pending = await p.dispatch_buffered({"operation": "auth.poll", "input": {"auth_id": "a1"}})
    # pending must carry no session; carrying one has the platform rewrite the credential row over and over
    assert pending == {"status": "pending"}

    assert await p.dispatch_buffered({"operation": "auth.submit", "input": {"auth_id": "a1", "input": "123456"}}) == {"ok": True}
    assert submitted == {"a1": "123456"}

    done = await p.dispatch_buffered({"operation": "auth.poll", "input": {"auth_id": "a1"}})
    assert done == {"status": "confirmed", "session": {"k": "v"}}


async def test_implementation_beyond_the_declaration_is_rejected():
    """qr has only start and poll. An extra submit is an implementation that is never called, refused on the
    spot."""
    p = Plugin({**CONTRACT, "auth_flow": {"kind": "qr", "steps": ["start", "poll"]}}, name="demo", token="t")
    with pytest.raises(ValueError):
        p.register_auth(
            start=lambda ctx: AuthChallenge(),
            poll=lambda ctx, aid: AuthState(),
            submit=lambda ctx, aid, v: None,
        )


async def test_auth_ops_do_not_collide_with_the_single_operation_fallback():
    """The single-operation fallback considers business operations only; an auth flow's reserved id must not
    be mistaken for the default."""
    p = make_plugin()
    p.register_auth(start=lambda ctx: AuthChallenge(prompt="x"), poll=lambda ctx, a: AuthState())
    p.register("noop", _noop)
    assert await p.dispatch_buffered({"operation": "", "input": {}}) == {"ok": True}


async def _noop(ctx, raw, out):
    out.vars({"ok": True})
