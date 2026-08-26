"""文件分块：字节不走操作 reply（受 max_payload 约束），走专用通道（协议 §6）。"""

import base64
import json
import types

from sokel.nats_transport import FILE_CHUNK, NatsFiles
from sokel.runtime import File


class FakeNC:
    """只实现 request：把每次请求记下来，按 subject 给出预设应答。"""

    def __init__(self, replies):
        self.sent = []
        self._replies = replies

    async def request(self, subject, data, timeout=None):
        req = json.loads(data)
        self.sent.append((subject, req))
        return types.SimpleNamespace(data=json.dumps(self._replies(subject, req)).encode())


async def test_fetch_walks_chunks_until_last():
    payload = b"0123456789"

    def reply(subject, req):
        assert subject == "sokel.file.get"
        half = payload[: 5] if req["seq"] == 0 else payload[5:]
        return {"data": base64.b64encode(half).decode(), "last": req["seq"] == 1}

    files = NatsFiles(FakeNC(reply), "skp_t")
    assert await files.fetch(File(id="f_1")) == payload


async def test_fetch_accepts_url_only_reference():
    """老引用只有 url：取末段当 id，而不是报一个「缺少 id」把调用打断。"""
    files = NatsFiles(FakeNC(lambda s, r: {"data": "", "last": True}), "t")
    await files.fetch(File(url="https://host/files/f_9"))
    assert files._nc.sent[0][1]["id"] == "f_9"


async def test_store_splits_into_chunks_and_returns_the_reference():
    data = b"x" * (FILE_CHUNK + 7)  # 一块多一点：必须发两趟

    def reply(subject, req):
        assert subject == "sokel.file.put"
        out = {"upload_id": "up_1"}
        if req["last"]:
            out["file"] = {"id": "f_2", "name": req["name"], "size": len(data)}
        return out

    nc = FakeNC(reply)
    f = await NatsFiles(nc, "t").store("big.bin", "application/octet-stream", data)
    seqs = [r["seq"] for _, r in nc.sent]
    assert seqs == [0, 1]
    assert [r["last"] for _, r in nc.sent] == [False, True]
    # 首块 upload_id 为空、平台回一个续用——续错就是每块各开一个会话，最后拼不出文件
    assert nc.sent[0][1]["upload_id"] == "" and nc.sent[1][1]["upload_id"] == "up_1"
    assert f.id == "f_2" and f.name == "big.bin"


async def test_store_empty_file_still_finishes_the_session():
    nc = FakeNC(lambda s, r: {"upload_id": "up", "file": {"id": "f_0"}} if r["last"] else {"upload_id": "up"})
    f = await NatsFiles(nc, "t").store("empty.txt", "text/plain", b"")
    assert len(nc.sent) == 1 and nc.sent[0][1]["last"] is True
    assert f.id == "f_0"


async def test_store_stream_walks_chunks_without_loading_everything():
    """边读边传：内存占用恒为一个块——视频这类几百 MB 的东西只能这么传。"""
    data = b"y" * (FILE_CHUNK * 2 + 3)  # 两块多一点

    def reply(subject, req):
        out = {"upload_id": "up_9"}
        if req["last"]:
            out["file"] = {"id": "f_stream", "name": req["name"]}
        return out

    import io

    nc = FakeNC(reply)
    f = await NatsFiles(nc, "t").store_stream("big.mp4", "video/mp4", io.BytesIO(data))
    assert [r["seq"] for _, r in nc.sent] == [0, 1, 2]
    assert [r["last"] for _, r in nc.sent] == [False, False, True]
    assert f.id == "f_stream"


async def test_upload_file_streams_from_disk(tmp_path):
    """Ctx.upload_file：给路径就行，mime 按扩展名猜。"""
    p = tmp_path / "clip.mp4"
    p.write_bytes(b"z" * 10)
    seen = {}

    class Rt:
        async def fetch(self, f):
            raise AssertionError("不该被调用")

        async def store(self, name, mime, data):
            raise AssertionError("大文件不该走整块上传")

        async def store_stream(self, name, mime, src):
            seen["name"], seen["mime"], seen["bytes"] = name, mime, src.read()
            return File(id="f_1", name=name)

    from sokel import Ctx

    f = await Ctx(files=Rt()).upload_file(str(p))
    assert seen == {"name": "clip.mp4", "mime": "video/mp4", "bytes": b"z" * 10}
    assert f.id == "f_1"
