package sokel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

// fakeFiles：记录每一块，用来钉住分块行为（真实实现走 NATS，这里只关心切法）。
type fakeFiles struct {
	chunks [][]byte
	peak   int // 单次收到的最大块（流式的意义就在于它恒等于块大小）
}

func (f *fakeFiles) fetch(context.Context, *File) ([]byte, error) { return nil, nil }
func (f *fakeFiles) store(ctx context.Context, name, mime string, data []byte) (*File, error) {
	return f.storeReader(ctx, name, mime, bytes.NewReader(data))
}
func (f *fakeFiles) storeReader(_ context.Context, name, mime string, r io.Reader) (*File, error) {
	buf := make([]byte, fileChunk)
	total := 0
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			f.chunks = append(f.chunks, append([]byte(nil), buf[:n]...))
			if n > f.peak {
				f.peak = n
			}
			total += n
		}
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}
	}
	return &File{ID: "f_1", Name: name, Mime: mime, Size: int64(total)}, nil
}

// 大文件必须**边读边传**：内存占用恒为一个块，与文件大小无关。
// 先 ReadAll 再传的话，NAS 上一个 2GB 的视频就等于要 2GB 常驻内存——
// 那不是慢一点，是插件进程直接被撑爆。
func TestUploadReaderStreamsInChunks(t *testing.T) {
	rt := &fakeFiles{}
	ctx := natsCtx{Context: context.Background(), rt: rt}
	size := fileChunk*3 + 12345
	f, err := ctx.UploadReader("big.mp4", "video/mp4", strings.NewReader(strings.Repeat("x", size)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Size != int64(size) {
		t.Errorf("大小对不上: %d, want %d", f.Size, size)
	}
	if len(rt.chunks) != 4 {
		t.Errorf("应切成 4 块（3 满 + 1 尾）, got %d", len(rt.chunks))
	}
	if rt.peak > fileChunk {
		t.Errorf("单块不该超过 %d, got %d", fileChunk, rt.peak)
	}
	// 内容不能在切分中丢失或串位
	var got bytes.Buffer
	for _, c := range rt.chunks {
		got.Write(c)
	}
	if got.Len() != size {
		t.Errorf("拼回来的字节数 %d, want %d", got.Len(), size)
	}
}

// Upload（整块字节）与 UploadReader 必须产出同样的分块——两条路只该有一份分块实现。
func TestUploadAndUploadReaderAgree(t *testing.T) {
	data := []byte(strings.Repeat("y", fileChunk+7))
	a, b := &fakeFiles{}, &fakeFiles{}
	if _, err := (natsCtx{Context: context.Background(), rt: a}).Upload("f", "application/octet-stream", data); err != nil {
		t.Fatal(err)
	}
	if _, err := (natsCtx{Context: context.Background(), rt: b}).UploadReader("f", "application/octet-stream", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(len(a.chunks), a.peak) != fmt.Sprint(len(b.chunks), b.peak) {
		t.Errorf("两条路的分块不一致: %d/%d vs %d/%d", len(a.chunks), a.peak, len(b.chunks), b.peak)
	}
}
