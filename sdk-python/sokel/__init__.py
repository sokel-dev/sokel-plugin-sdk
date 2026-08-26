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

# 版本从**已安装的分发元数据**取，不在这里再写一遍。
# 写死的那份是版本号的第四处副本（另三处：pyproject / package.json / git tag），
# 而它偏偏是最容易漏的——0.2.0 发出去时它还写着 0.1.0，分发说 0.2.0、模块说 0.1.0。
try:
    from importlib.metadata import version as _dist_version

    __version__ = _dist_version("sokel-plugin-sdk")
except Exception:  # 直接在源码树里跑（没装）时没有元数据
    __version__ = "0.0.0+local"
