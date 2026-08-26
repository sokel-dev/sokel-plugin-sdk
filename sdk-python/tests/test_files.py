"""File chunking: bytes do not travel on an operation reply, which max_payload bounds, but on a dedicated
channel (§6 of the protocol)."""

import base64
import json
import types

from sokel.nats_transport import FILE_CHUNK, NatsFiles
from sokel.runtime import File


class FakeNC:
    """Implements request only: records every request and answers from a table keyed by subject."""

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
    """An older reference carries only a url: take its last segment as the id rather than breaking the call
    with "no id"."""
    files = NatsFiles(FakeNC(lambda s, r: {"data": "", "last": True}), "t")
    await files.fetch(File(url="https://host/files/f_9"))
    assert files._nc.sent[0][1]["id"] == "f_9"


async def test_store_splits_into_chunks_and_returns_the_reference():
    data = b"x" * (FILE_CHUNK + 7)  # just over one chunk, so it takes two rounds

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
    # The first chunk sends no upload_id and the platform returns one to continue with; getting that wrong
    # opens a session per chunk and the file never reassembles
    assert nc.sent[0][1]["upload_id"] == "" and nc.sent[1][1]["upload_id"] == "up_1"
    assert f.id == "f_2" and f.name == "big.bin"


async def test_store_empty_file_still_finishes_the_session():
    nc = FakeNC(lambda s, r: {"upload_id": "up", "file": {"id": "f_0"}} if r["last"] else {"upload_id": "up"})
    f = await NatsFiles(nc, "t").store("empty.txt", "text/plain", b"")
    assert len(nc.sent) == 1 and nc.sent[0][1]["last"] is True
    assert f.id == "f_0"


async def test_store_stream_walks_chunks_without_loading_everything():
    """Streaming while reading: memory use is always one chunk, which is the only way to send something the
    size of a video."""
    data = b"y" * (FILE_CHUNK * 2 + 3)  # just over two chunks

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
    """Ctx.upload_file takes a path, guessing the mime type from the extension."""
    p = tmp_path / "clip.mp4"
    p.write_bytes(b"z" * 10)
    seen = {}

    class Rt:
        async def fetch(self, f):
            raise AssertionError("should not be called")

        async def store(self, name, mime, data):
            raise AssertionError("a large file must not go through the whole-bytes upload")

        async def store_stream(self, name, mime, src):
            seen["name"], seen["mime"], seen["bytes"] = name, mime, src.read()
            return File(id="f_1", name=name)

    from sokel import Ctx

    f = await Ctx(files=Rt()).upload_file(str(p))
    assert seen == {"name": "clip.mp4", "mime": "video/mp4", "bytes": b"z" * 10}
    assert f.id == "f_1"
