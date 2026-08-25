package sokel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"io"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// File 文件引用（定义在 plugin-core/plugin —— 它只是数据，取字节依赖传输）。
//
//	入参：in.SomeFile.Blob(ctx) 惰性取字节（SDK 经 NATS 分块从平台拉取）。
//	出参：f, _ := ctx.Upload("r.json", mime, data) → out.Vars(Out{Report: f})。
type File = plugin.File

// Fetch 实现 plugin.Ctx：经平台文件层分块拉取字节。File.Blob 会调它。
func (c natsCtx) Fetch(f *File) ([]byte, error) {
	if c.rt == nil {
		return nil, errors.New("文件运行时未就绪")
	}
	return c.rt.fetch(c.Context, f)
}

// Upload 产出一个文件：字节交回平台登记，返回可放入出参 struct 的引用。
func (c natsCtx) Upload(name, mime string, data []byte) (*File, error) {
	if c.rt == nil {
		return &File{Name: name, Mime: mime, Size: int64(len(data)), Data: data}, nil
	}
	return c.rt.store(c.Context, name, mime, data)
}

// UploadReader 边读边传：内存占用恒为一个块（1MB），与文件大小无关。
// 几百 MB 以上的东西一律走它——Upload 要先把整个文件读进内存，那会撑爆插件进程。
func (c natsCtx) UploadReader(name, mime string, r io.Reader) (*File, error) {
	if c.rt == nil { // 裸 ctx（测试）：读进内存当作直接产出
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return &File{Name: name, Mime: mime, Size: int64(len(b)), Data: b}, nil
	}
	return c.rt.storeReader(c.Context, name, mime, r)
}

// fileRuntime 文件字节的取/存后端，由传输层注入。
type fileRuntime interface {
	fetch(ctx context.Context, f *File) ([]byte, error)
	store(ctx context.Context, name, mime string, data []byte) (*File, error)
	storeReader(ctx context.Context, name, mime string, r io.Reader) (*File, error)
}

// natsFiles：经已有 NATS 连接与平台交换文件字节（1MB/块，逐块 request-reply）。
// 不要求插件可达平台 HTTP —— 内网插件同样可用。
type natsFiles struct {
	nc    *nats.Conn
	token string
}

const fileChunk = 1 << 20

func (n natsFiles) fetch(_ context.Context, f *File) ([]byte, error) {
	id := f.ID
	if id == "" { // 兼容仅有 url 的引用：取末段作为 id
		if i := strings.LastIndex(f.URL, "/"); i >= 0 {
			id = f.URL[i+1:]
		}
	}
	if id == "" {
		return nil, errors.New("文件引用缺少 id/url")
	}
	var out []byte
	for seq := 0; ; seq++ {
		req, _ := json.Marshal(map[string]any{"token": n.token, "id": id, "seq": seq})
		resp, err := n.nc.Request("sokel.file.get", req, 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("拉取文件块 %d: %w", seq, err)
		}
		var r struct {
			Error string `json:"error"`
			Data  string `json:"data"`
			Last  bool   `json:"last"`
		}
		if err := json.Unmarshal(resp.Data, &r); err != nil {
			return nil, err
		}
		if r.Error != "" {
			return nil, errors.New(r.Error)
		}
		b, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
		if r.Last {
			return out, nil
		}
	}
}

// store：整块字节。走 storeReader —— 分块协议只该有一份实现。
func (n natsFiles) store(ctx context.Context, name, mime string, data []byte) (*File, error) {
	return n.storeReader(ctx, name, mime, bytes.NewReader(data))
}

// storeReader：**边读边传**，内存占用恒为一个块。
//
// 几百 MB 的文件（NAS 上的视频/压缩包）用 store 那种「先读进内存再传」会把插件进程撑爆；
// 平台那侧本来就是逐块写进 blob writer 的，瓶颈一直只在插件这边。
func (n natsFiles) storeReader(_ context.Context, name, mime string, r io.Reader) (*File, error) {
	uploadID := ""
	buf := make([]byte, fileChunk)
	for seq := 0; ; seq++ {
		nRead, rerr := io.ReadFull(r, buf)
		// ReadFull 只在读满或读到底时返回；未满即到底是正常收尾，不是错误
		if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("读取第 %d 块: %w", seq, rerr)
		}
		last := rerr == io.EOF || rerr == io.ErrUnexpectedEOF
		// 空文件也要走一轮（last=true, 0 字节），否则平台侧没有会话可收尾
		if nRead == 0 && seq > 0 {
			last = true
		}
		req, _ := json.Marshal(map[string]any{
			"token": n.token, "upload_id": uploadID, "name": name, "mime": mime,
			"seq": seq, "last": last, "data": base64.StdEncoding.EncodeToString(buf[:nRead]),
		})
		resp, err := n.nc.Request("sokel.file.put", req, 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("上传文件块 %d: %w", seq, err)
		}
		var rr struct {
			Error    string `json:"error"`
			UploadID string `json:"upload_id"`
			File     *File  `json:"file"`
		}
		if err := json.Unmarshal(resp.Data, &rr); err != nil {
			return nil, err
		}
		if rr.Error != "" {
			return nil, errors.New(rr.Error)
		}
		if rr.UploadID != "" {
			uploadID = rr.UploadID
		}
		if last {
			if rr.File == nil {
				return nil, errors.New("平台未返回文件引用")
			}
			return rr.File, nil
		}
	}
}
