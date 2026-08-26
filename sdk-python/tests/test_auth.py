"""协作式认证：步骤由声明定死，实现只挂在保留操作 id 上。"""

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
        start=lambda ctx: AuthChallenge(auth_id="a1", prompt="填验证码", expires_in=60),
        poll=lambda ctx, aid: AuthState(status="confirmed", session={"k": "v"}) if submitted else AuthState(status="pending"),
        submit=lambda ctx, aid, value: submitted.update({aid: value}),
    )

    start = await p.dispatch_buffered({"operation": "auth.start", "input": {}})
    assert start["auth_id"] == "a1"
    assert start["challenge"] == {"kind": "input", "qr_image": "", "prompt": "填验证码"}
    assert start["expires_in"] == 60

    pending = await p.dispatch_buffered({"operation": "auth.poll", "input": {"auth_id": "a1"}})
    # pending 时不能带 session：带了等于让平台反复覆写凭证行
    assert pending == {"status": "pending"}

    assert await p.dispatch_buffered({"operation": "auth.submit", "input": {"auth_id": "a1", "input": "123456"}}) == {"ok": True}
    assert submitted == {"a1": "123456"}

    done = await p.dispatch_buffered({"operation": "auth.poll", "input": {"auth_id": "a1"}})
    assert done == {"status": "confirmed", "session": {"k": "v"}}


async def test_implementation_beyond_the_declaration_is_rejected():
    """qr 只有 start+poll。多写一个 submit = 一份永远不会被调用的实现，当场拦住。"""
    p = Plugin({**CONTRACT, "auth_flow": {"kind": "qr", "steps": ["start", "poll"]}}, name="demo", token="t")
    with pytest.raises(ValueError):
        p.register_auth(
            start=lambda ctx: AuthChallenge(),
            poll=lambda ctx, aid: AuthState(),
            submit=lambda ctx, aid, v: None,
        )


async def test_auth_ops_do_not_collide_with_the_single_operation_fallback():
    """单操作插件的兜底只认业务操作——认证流的保留 id 不能被它误当成默认操作。"""
    p = make_plugin()
    p.register_auth(start=lambda ctx: AuthChallenge(prompt="x"), poll=lambda ctx, a: AuthState())
    p.register("noop", _noop)
    assert await p.dispatch_buffered({"operation": "", "input": {}}) == {"ok": True}


async def _noop(ctx, raw, out):
    out.vars({"ok": True})
