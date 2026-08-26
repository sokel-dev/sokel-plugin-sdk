"""Sokel plugin SDK for Python.

契约在 sokel.yaml 里声明（语言中立），`sokel-gen generate` 生成类型化的模型与注册口；
本包提供运行时：注册握手、心跳、调用分发、文件分块、事件触发、webhook、协作式认证。

    from sokel import Plugin
    from sokel_gen import CONTRACT, on_issues_list, IssuesListIn, IssuesListOut

    p = Plugin(CONTRACT, name="gitlab")

    async def handle(ctx, in_: IssuesListIn) -> IssuesListOut:
        ...

    on_issues_list(p, handle)
    asyncio.run(p.run())
"""

from .auth import AuthChallenge, AuthState
from .contract import Contract
# 注意导出名不能叫 env —— 那会遮住 sokel.env 这个子模块，包内的 `from . import env`
# 会拿到函数而不是模块（第一次跑示例就撞上了，报的还是个莫名其妙的 AttributeError）。
from .env import get as getenv, get_or as getenv_or
from .events import CredEntry, Source, SourceCtx, StateBoard
from .plugin import Plugin
from .runtime import Ctx, Emitter, File
from .webhook import WebhookRequest, WebhookResponse, ok, text

__all__ = [
    "AuthChallenge",
    "AuthState",
    "Contract",
    "CredEntry",
    "Ctx",
    "Emitter",
    "File",
    "Plugin",
    "Source",
    "SourceCtx",
    "StateBoard",
    "WebhookRequest",
    "WebhookResponse",
    "getenv",
    "getenv_or",
    "ok",
    "text",
]

__version__ = "0.1.0"
