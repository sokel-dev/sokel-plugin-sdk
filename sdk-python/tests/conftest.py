import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
# The reference plugin's generated output: the Python consistency suite reads it directly rather than
# building a second contract
sys.path.insert(0, str(ROOT / "examples" / "kitchen-sink" / "python"))


def golden() -> dict:
    return json.loads((ROOT / "examples" / "kitchen-sink" / "contract.golden.json").read_text())


SIMPLE_CONTRACT = {
    "name": "demo",
    "operations": [
        {
            "id": "greet",
            "label": "Say hello",
            "inputs": [{"name": "who", "type": "string", "required": True}],
            "outputs": [{"name": "text", "type": "string", "required": True}],
        },
        {
            "id": "stream_it",
            "label": "Streaming",
            "stream": True,
            "inputs": [],
            "outputs": [{"name": "n", "type": "number", "required": True}],
        },
    ],
    "events": [{"id": "ping", "fields": [{"name": "at", "type": "string"}]}],
}
