// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package sdk 只做一件事：把「写一个插件需要读的东西」编进 sokel-gen 二进制。
//
// 为什么要编进去：`go install` 装来的 sokel-gen 手边没有这个仓库。
// 而需要读这些东西的往往不是人——让 AI 照着写插件时，它能执行命令、拿到 stdout，
// 却未必能访问 GitHub。`sokel-gen docs` / `sokel-gen example` 就是给它准备的入口。
//
// 这里**只引用不复制**：文档、schema、参考声明都还是仓库里那一份，
// 编进二进制的是同一个文件——复制一份的话，两份迟早不一样，而读到旧那份的人不会知道。
package sdk

import _ "embed"

// ManifestDoc sokel.yaml 的写法说明（docs/manifest.md）。
//
//go:embed docs/manifest.md
var ManifestDoc string

// Schema sokel.yaml 的 JSON Schema：编辑器补全用，也能喂给会读 schema 的工具。
//
//go:embed docs/sokel.schema.json
var Schema string

// ExampleManifest 覆盖全部契约形态的参考声明（examples/kitchen-sink/sokel.yaml）。
//
//go:embed examples/kitchen-sink/sokel.yaml
var ExampleManifest string

// ExamplePython 与上面那份声明配套的 Python 实现。
//
//go:embed examples/kitchen-sink/python/main.py
var ExamplePython string

// ExampleNode 与上面那份声明配套的 TypeScript 实现。
//
//go:embed examples/kitchen-sink/node/src/main.ts
var ExampleNode string
