// sysinfo 插件：返回「插件运行系统」的基础信息。
//
// 用 sokel SDK 编写：类型化 In/Out struct 声明契约（反射自动上报平台），Emitter 产出结果，
// 完全不碰底层传输 —— 只有 Config.Endpoint/Transport 决定实际怎么部署。
//
// 运行：
//
//	SOKEL_ENDPOINT=http://localhost:8088 SOKEL_TOKEN=skp_xxx go run .
package main

//go:generate go run github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen
// 契约由 zz_types.go / zz_register.go 提供（编译期生成，非运行时反射）。改了 schema/ 后须重新生成。

import (
	"crypto/md5"
	"encoding/hex"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/sokel-dev/sokel-plugin-sdk/examples/sysinfo/schema"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
)

var startedAt = time.Now()

func main() {
	token := sokel.Env("TOKEN")
	if token == "" {
		log.Fatal("请设置 SOKEL_TOKEN（插件管理里该插件的接入 token）")
	}
	p := sokel.New(sokel.Config{
		Endpoint: sokel.EnvOr("ENDPOINT", "http://localhost:8088"),
		Token:    token,
		Name:     "sysinfo-plugin",
	})
	p.SetDoc(usageDoc, "") // 使用说明（docs/*.md）：随握手上报，界面「使用说明」显示它

	OnSystemInfo(p, func(ctx sokel.Ctx, in *SystemInfoIn) (*SystemInfoOut, error) {
		now := time.Now()
		host, _ := os.Hostname()
		o := &SystemInfoOut{
			OK: true, Hostname: host, OS: runtime.GOOS, Arch: runtime.GOARCH,
			NumCPU: runtime.NumCPU(), GoVersion: runtime.Version(), PID: os.Getpid(),
			Goroutines: runtime.NumGoroutine(), UptimeSeconds: int(now.Sub(startedAt).Seconds()),
			StartedAt: startedAt.UTC().Format(time.RFC3339), Now: now.UTC().Format(time.RFC3339),
			Echo: in.Note,
		}
		if in.IncludeMemory {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			o.Memory = schema.Memory{AllocBytes: m.Alloc, SysBytes: m.Sys, HeapAllocBytes: m.HeapAlloc}
		}
		return o, nil
	})

	// 文件摘要：演示文件入参 —— in.File.Blob(ctx) 惰性取字节（对齐 Dify file.blob）。
	OnFileDigest(p, func(ctx sokel.Ctx, in *FileDigestIn) (*FileDigestOut, error) {
		b, err := in.File.Blob(ctx)
		if err != nil {
			return nil, err
		}
		sum := md5.Sum(b)
		return &FileDigestOut{Filename: in.File.Name, MD5: hex.EncodeToString(sum[:]), Size: len(b)}, nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
