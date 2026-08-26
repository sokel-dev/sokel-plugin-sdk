import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
# 参考插件的生成物：Python 侧的一致性套件直接吃它，而不是另造一份契约
sys.path.insert(0, str(ROOT / "examples" / "kitchen-sink" / "python"))


def golden() -> dict:
    return json.loads((ROOT / "examples" / "kitchen-sink" / "contract.golden.json").read_text())


SIMPLE_CONTRACT = {
    "name": "demo",
    "operations": [
        {
            "id": "greet",
            "label": "打招呼",
            "inputs": [{"name": "who", "type": "string", "required": True}],
            "outputs": [{"name": "text", "type": "string", "required": True}],
        },
        {
            "id": "stream_it",
            "label": "流式",
            "stream": True,
            "inputs": [],
            "outputs": [{"name": "n", "type": "number", "required": True}],
        },
    ],
    "events": [{"id": "ping", "fields": [{"name": "at", "type": "string"}]}],
}
