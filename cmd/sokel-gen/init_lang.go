package main

// Python / Node 插件的骨架。
//
// 与 Go 骨架的唯一结构差别：契约声明在 sokel.yaml（语言中立）而不是 schema/ 包。
// 其余纪律一致——两份文档都在（README 给改代码的人、docs/ 给用户），
// 骨架里那个 hello 是**真的能跑通全链**的操作，不是占位注释。

import "strings"

func scaffoldPython(name string) map[string]string {
	r := strings.NewReplacer("{{name}}", name)
	return map[string]string{
		"sokel.yaml":           r.Replace(manifestTemplate) + "codegen:\n  - { lang: python, out: sokel_gen.py }\n",
		"main.py":              r.Replace(pyMain),
		"requirements.txt":     "sokel-plugin-sdk>=0.2\n",
		"docs/" + name + ".md": r.Replace(userDoc),
		"README.md":            r.Replace(devDoc) + r.Replace(pyDevDoc),
		".gitignore":           "__pycache__/\n.venv/\n.sokel-instance-id*\n",
	}
}

func scaffoldTS(name string) map[string]string {
	r := strings.NewReplacer("{{name}}", name)
	return map[string]string{
		"sokel.yaml":           r.Replace(manifestTemplate) + "codegen:\n  - { lang: ts, out: src/sokel.gen.ts }\n",
		"src/main.ts":          r.Replace(tsMain),
		"package.json":         r.Replace(tsPackage),
		"tsconfig.json":        tsConfig,
		"docs/" + name + ".md": r.Replace(userDoc),
		"README.md":            r.Replace(devDoc) + r.Replace(tsDevDoc),
		".gitignore":           "node_modules/\ndist/\n.sokel-instance-id*\n",
	}
}

const manifestTemplate = `# yaml-language-server: $schema=https://raw.githubusercontent.com/sokel-dev/sokel-plugin-sdk/main/docs/sokel.schema.json
# {{name}} 的契约声明 —— 语言中立，改这里。
#
# 改完重跑 sokel-gen generate .（生成物是类型化的模型与注册口，别手改）。
# 字段能声明什么：见 SDK 的 docs/manifest.md；一份覆盖全部形态的例子在
# examples/kitchen-sink/sokel.yaml。

plugin:
  name: {{name}}
  label: {{name}}
  version: 0.1.0
  doc: docs/{{name}}.md

# 凭证契约：平台随每次调用把解析后的字段下发给插件，插件从不落地凭证。
# 用不上就整段删掉。
credential:
  fields:
    - { name: api_key, label: API Key, type: secret, required: true }

operations:
  - id: hello
    label: 打招呼
    desc: 给一个名字，回一句招呼语——换成你自己的第一个操作。
    inputs:
      - { name: name, label: 名字, type: string, required: true }
    outputs:
      - { name: greeting, label: 招呼语, type: string, required: true }

`

const pyMain = `"""{{name}} —— Sokel 插件（Python）。

插件是**出站拨入**的：它主动连回平台，不需要开放入站端口或公网 IP。

运行：

    SOKEL_ENDPOINT=nats://<broker>:4222 SOKEL_TOKEN=skp_xxx python main.py
"""

import asyncio
import logging

from sokel import Ctx

# 由 sokel.yaml 生成：改了声明就重跑 sokel-gen generate .
from sokel_gen import HelloIn, HelloOut, new_plugin, on_hello

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s")

p = new_plugin()


async def hello(ctx: Ctx, in_: HelloIn) -> HelloOut:
    return HelloOut(greeting=f"你好，{in_.name}")


on_hello(p, hello)


if __name__ == "__main__":
    asyncio.run(p.run())
`

const tsMain = `/**
 * {{name}} —— Sokel 插件（TypeScript）。
 *
 * 插件是**出站拨入**的：它主动连回平台，不需要开放入站端口或公网 IP。
 *
 * 运行：
 *
 *     SOKEL_ENDPOINT=nats://<broker>:4222 SOKEL_TOKEN=skp_xxx node dist/main.js
 */

import type { Ctx } from "@sokel-dev/plugin-sdk";

// 由 sokel.yaml 生成：改了声明就重跑 sokel-gen generate .
import { newPlugin, onHello } from "./sokel.gen.js";
import type { HelloIn, HelloOut } from "./sokel.gen.js";

const p = newPlugin();

onHello(p, (_ctx: Ctx, in_: HelloIn): HelloOut => ({ greeting: ` + "`你好，${in_.name}`" + ` }));

await p.run();
`

const tsPackage = `{
  "name": "{{name}}",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "tsc -p tsconfig.json",
    "start": "node dist/main.js"
  },
  "dependencies": {
    "@sokel-dev/plugin-sdk": "^0.2.0"
  },
  "devDependencies": {
    "@types/node": "^22.10.0",
    "typescript": "^5.7.0"
  }
}
`

const tsConfig = `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "lib": ["ES2023"],
    "strict": true,
    "outDir": "dist",
    "rootDir": "src",
    "types": ["node"],
    "skipLibCheck": true
  },
  "include": ["src/**/*.ts"]
}
`

const userDoc = `# {{name}}

> 给**用户**看的：这个插件能做什么、要填什么、有什么坑。
> 给改代码的人看的在 README.md。

## 能做什么

- **打招呼**：给一个名字，回一句招呼语。

## 凭证

| 字段 | 必填 | 说明 |
|---|---|---|
| API Key | 是 | 上游服务的密钥；脱敏存储 |
`

const devDoc = `# {{name}}

> 给**改代码的人**看的。给用户看的在 ` + "`docs/{{name}}.md`" + `。

## 结构

| 文件 | 作用 |
|---|---|
| ` + "`sokel.yaml`" + ` | 契约声明——有哪些操作、收什么回什么。**改这里** |
| 生成物 | 由声明生成的类型与注册口。**别手改** |
| 入口文件 | handler 实现 + 连回平台的接线 |
| ` + "`docs/{{name}}.md`" + ` | 给用户的说明，随注册握手上报，界面「使用说明」显示它 |

契约是**生成**的不是手写的：改了 ` + "`sokel.yaml`" + ` 忘了重新生成，
` + "`sokel-gen check .`" + ` 会红——这是 codegen 最常见的失效方式，CI 拦这一道。

`

const pyDevDoc = "## 开发\n\n```bash\npip install -r requirements.txt\nsokel-gen generate .   # 改了 sokel.yaml 就重新生成\npython main.py\nsokel-gen check .      # CI 用\n```\n"

const tsDevDoc = "## 开发\n\n```bash\nnpm install\nsokel-gen generate .   # 改了 sokel.yaml 就重新生成\nnpm run build && npm start\nsokel-gen check .      # CI 用\n```\n"
