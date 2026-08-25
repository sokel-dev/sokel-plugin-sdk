package sokel

// traceTag 要认得 trace_id（模型/插件调用的请求级 trace，平台 0137 起下发）：
// 平台日志一行 [tr_x]、插件日志一行 [tr=tr_x]，两边才能拿同一串 id 对账。
import "testing"

func TestTraceTagCarriesTraceID(t *testing.T) {
	got := traceTag(map[string]string{"run_id": "run_1", "trace_id": "tr_abc"})
	want := " [run=run_1 tr=tr_abc]"
	if got != want {
		t.Errorf("traceTag = %q, want %q", got, want)
	}
	if traceTag(nil) != "" {
		t.Error("空上下文应为空串")
	}
}
