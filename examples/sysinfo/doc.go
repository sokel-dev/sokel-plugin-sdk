package main

// 使用说明：真的 markdown 文件，编译期 embed 进来（见 docs/dev-playbook.md §5.0.0）。

import _ "embed"

//go:embed docs/sysinfo.md
var usageDoc string
