# 发布

**一个 tag 发三个 SDK**，版本号一致。

| SDK | 发到哪 | 怎么发 |
|---|---|---|
| Go | 无需发布 | `go get` 直接按 git tag 取 —— **推 tag 即发布** |
| Python | PyPI `sokel-plugin-sdk` | GitHub Actions（`.github/workflows/release.yml`） |
| Node | npm `@sokel-dev/plugin-sdk` | 同上 |

为什么绑在一起：三者实现的是同一版线协议，一致性靠 `examples/kitchen-sink` 的 golden 保住。
各发各的版本，「哪几个版本互相对得上」就成了要人记的事——而没人会记，直到某天两边行为不一致。

## 一次性配置（三步，都在网页上点）

### 1. npm（可信发布，已配好）

两个仓库现在都走 OIDC，**这条链路上没有任何长期机密**。

npm 组织名与包名的作用域一致：包是 `@sokel-dev/plugin-sdk`，所以组织叫 `sokel-dev`（与 GitHub 组织
同名，省一件要记的事）。

可信发布配在**包的设置页**里：

| 字段 | 值 |
|---|---|
| Organization or user | `sokel-dev` |
| Repository | `sokel-plugin-sdk` |
| Workflow filename | `release.yml`（只要文件名，带扩展名） |
| Environment | `release` |
| Allowed actions | `npm publish` |

工作流为此做了两件事，改流水线时最容易丢掉：

- `publish-npm` 那个 job 上的 `id-token: write`。没有它就没有可以出示的 OIDC token。
- 发布前的 `npm install -g npm@latest`。可信发布要求 npm >= 11.5.1，而 Node 22 自带的 npm 更老；
  不升级的话 npm 会退回去找 token 并报 401——看起来像凭证问题，其实是版本问题。

provenance（发布来源可验证）是白送的：走 OIDC 发布时 npm 自动带上，不必加 `--provenance`。

<details>
<summary>以后要在新作用域下发一个全新的包时</summary>

可信发布配在包的设置页里，而包不存在时没有那个页面——所以全新包名的**第一次**发布只能走 token：

- Access Tokens → Generate。**granular（细粒度）token 就够**：Permissions 选 Read and write，
  Select packages and scopes 选那个作用域（最小权限）。
  ⚠️ **Expiration 一定要填一个真日期**——留空会落到「今天」，token 生成出来当天就过期，
  而 CI 会报成 401，看起来像「token 填错了」。
- **token 只显示一次**，生成后直接贴进 GitHub：仓库 Settings → Environments → `release` →
  Add environment secret，Name 填 `NPM_TOKEN`，再给发布那一步加上
  `NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}`。
  放在 environment 而不是仓库级 secret：只有跑在 `release` 环境里的那个 job 读得到，
  别的 workflow（包括 PR 触发的）碰不到它。
- 然后照上面把可信发布配好，删掉那个 `env:` 块，并**把 secret 删掉**。

想在本机确认这类 token 能用：

```bash
echo "//registry.npmjs.org/:_authToken=<token>" >> ~/.npmrc
npm whoami            # 打出你的用户名就是通的
npm org ls sokel-dev  # 能列出成员 = 作用域权限也对
```

别把 token 贴进聊天、终端历史或仓库里的任何文件——它等价于「以你的身份发包」。

</details>

`package.json` 里已经写死 `publishConfig.access = public`：作用域包默认是 restricted，
不显式声明的话第一次 publish 会以「需要付费账户」失败，而错因跟真实原因完全不搭。

### 2. PyPI

用 **可信发布（Trusted Publisher）**，不需要任何 token：

PyPI 与 npm 相反：项目**还不存在时就能配**（pending publisher），所以从第一版起就不需要 token。
账号侧边栏 → Publishing → Add a new pending publisher（注意是账号页，不是项目页——项目还没有）：

| 字段 | 值 |
|---|---|
| PyPI Project Name | `sokel-plugin-sdk` |
| Owner | `sokel-dev` |
| Repository name | `sokel-plugin-sdk` |
| Workflow name | `release.yml` |
| Environment name | `release`（可选，但强烈建议填——它把「谁能发」收窄到这个环境） |

⚠️ pending publisher **不会占名**：真正发出第一版之前，别人仍可能注册走 `sokel-plugin-sdk`。
所以配好之后尽快发一版。

### 3. GitHub

仓库 Settings → Environments 建一个 `release`（名字要与上表一致）。
可以在这里加人工审批——发布是不可撤销的（npm 与 PyPI 都不允许重发同一个版本号），
多一道确认便宜。

## 发一版

```bash
# 1. 改两处版本号（tag 是第三处，CI 会核对三者一致）
vi sdk-node/package.json      # "version": "0.2.0"
vi sdk-python/pyproject.toml  # version = "0.2.0"

# 2. 本地先过一遍闸（与 CI 同一组命令）
go test ./... && go run ./cmd/sokel-gen check ./examples
(cd sdk-node && npm test)
(cd sdk-python && python -m pytest -q)

# 3. 提交、打 tag、推
git commit -am "chore: v0.2.0"
git tag v0.2.0
git push origin main --tags
```

推上去之后：`verify` 跑全套闸 → 过了才发 npm 与 PyPI。
Go 那侧不需要等流水线——tag 推上去 `go get github.com/sokel-dev/sokel-plugin-sdk@v0.2.0` 就能取到。

想空跑一遍（不发布）：Actions → Release → Run workflow，填个版本号即可，只跑 `verify`。

## 流水线拦的是什么

每一条都对应一种**发出去才会发现**的失效：

| 闸 | 拦的问题 |
|---|---|
| 版本一致性 | 包版本与 tag 对不上 → 之后没法从代码追回「这个包是哪次提交」 |
| `sokel-gen check ./examples` | 改了声明没重新生成 → 发出去的 SDK 与仓库里的示例对不上 |
| 三语言测试 + golden | 某个 SDK 对协议的理解漂了 |
| `npm pack --dry-run` 里有 `dist/src/index.js` | `dist` 不在版本库里，忘了 build 就发出一个**空包**——装上去照样成功，import 才炸 |
| sdist 里有 `sokel/plugin.py` | 同上的 Python 版（`packages` 配错时装上去是个空壳） |
| PyPI 发的是 verify 构建的**同一批文件** | 重新构建就可能与刚检过的不是同一个包 |

## 发完对一下

```bash
npm view @sokel-dev/plugin-sdk version
pip index versions sokel-plugin-sdk
go list -m github.com/sokel-dev/sokel-plugin-sdk@v0.2.0
```

`sokel-gen init` 生成的骨架里写的是 `sokel-plugin-sdk>=0.2` 与 `@sokel-dev/plugin-sdk: ^0.2.0`——
主版本号跳动时记得同步 `cmd/sokel-gen/init_lang.go` 里那两行。
