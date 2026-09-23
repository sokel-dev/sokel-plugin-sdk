package sokel

// 一个副本要能同时服务多个调用（用户实报 2026-09-23）。
//
// 此前回调里直接跑 dispatchNATS，而 nats.go 的异步订阅**每个订阅只有一条派发 goroutine**
// （go nc.waitForMsgs(sub) → 循环里同步调回调），于是一个副本同一时刻只处理一个请求：
// 38 秒的 PDF 解析期间，那个副本对所有人装死——连凭证体检都排在后面超时，
// 平台报「插件(NATS)无响应 … context deadline exceeded」，解析一结束又自己好了。

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestConcurrentHandlerRunsCallsInParallel(t *testing.T) {
	const n = 5
	var running, peak int64
	var wg sync.WaitGroup
	wg.Add(n)
	h := concurrentHandler(n, func(*nats.Msg) {
		cur := atomic.AddInt64(&running, 1)
		for { // 记录同时在跑的峰值
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond) // 慢调用：串行的话总耗时会是 n 倍
		atomic.AddInt64(&running, -1)
		wg.Done()
	})
	start := time.Now()
	for i := 0; i < n; i++ {
		h(&nats.Msg{Reply: "r"})
	}
	wg.Wait()
	if p := atomic.LoadInt64(&peak); p < 2 {
		t.Fatalf("同时在跑的峰值只有 %d——派发仍是串行的，一个慢调用会让整个副本装死", p)
	}
	if el := time.Since(start); el > 300*time.Millisecond {
		t.Errorf("%d 个 80ms 的调用花了 %v，接近串行", n, el)
	}
}

// 缺省不设上限：计量是运维的事（平台侧已有按通道的并发与限流），插件不替人做主。
func TestConcurrentHandlerUncappedByDefault(t *testing.T) {
	var running, peak int64
	var wg sync.WaitGroup
	const total = 12
	wg.Add(total)
	h := concurrentHandler(0, func(*nats.Msg) { // 0 = 不设上限
		cur := atomic.AddInt64(&running, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt64(&running, -1)
		wg.Done()
	})
	for i := 0; i < total; i++ {
		h(&nats.Msg{Reply: "r"})
	}
	wg.Wait()
	if p := atomic.LoadInt64(&peak); p < total/2 {
		t.Fatalf("不设上限时同时在跑的峰值只有 %d/%d——说明哪里还在排队", p, total)
	}
}

// 设了上限就要真的封顶（给「一份文档就是一整个内存」那类处理函数用）。
// 等待发生在**调用方**（订阅的派发 goroutine）——多出来的消息留在订阅的 pending 缓冲里。
func TestConcurrentHandlerCapsInFlightWhenSet(t *testing.T) {
	var running, peak int64
	var wg sync.WaitGroup
	const total, limit = 8, 2
	wg.Add(total)
	h := concurrentHandler(limit, func(*nats.Msg) {
		cur := atomic.AddInt64(&running, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt64(&running, -1)
		wg.Done()
	})
	for i := 0; i < total; i++ {
		h(&nats.Msg{Reply: "r"})
	}
	wg.Wait()
	if p := atomic.LoadInt64(&peak); p > limit {
		t.Fatalf("同时在跑 %d 个，超过上限 %d——无界并发会把副本的内存吃光", p, limit)
	}
}

// 逃生门：并发不安全的处理函数可以退回串行（SOKEL_MAX_CONCURRENCY=1）。
func TestDispatchConcurrencyFromEnv(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{
		{"", 0},  // 缺省不设闸：计量是运维的事，平台侧已有按通道的并发与限流
		{"0", 0}, // 显式 0 = 不设闸（与缺省同义）
		{"1", 1}, // 逃生门：退回严格串行，给并发不安全的处理函数
		{"16", 16},
		{"abc", 0}, // 写错了退回「不设闸」并留日志，不是静默掐成 1
		{" 4 ", 4}, // 容忍空白（compose 里常见）
	} {
		t.Setenv("SOKEL_MAX_CONCURRENCY", c.env)
		if got := dispatchConcurrency(); got != c.want {
			t.Errorf("SOKEL_MAX_CONCURRENCY=%q → %d，want %d", c.env, got, c.want)
		}
	}
}
