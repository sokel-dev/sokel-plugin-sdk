package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/payload"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
)

func payloadPlugin(t *testing.T, pf *platform, invoked ...func()) {
	t.Helper()
	p := sokel.New(sokel.Config{Endpoint: pf.http.URL, Token: "skp_it"})
	p.Register(contract.Operation{ID: "echo", Label: "echo"}, func(ctx plugin.Ctx, raw json.RawMessage, sink plugin.Sink) error {
		var input struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return err
		}
		if input.Text != "ready" && len(invoked) > 0 {
			invoked[0]()
		}
		sink.Vars(struct {
			Text string `sokel:"text"`
		}{input.Text})
		return nil
	})
	p.Register(contract.Operation{ID: "huge", Label: "huge"}, func(ctx plugin.Ctx, raw json.RawMessage, sink plugin.Sink) error {
		sink.Vars(struct {
			Text string `sokel:"text"`
		}{strings.Repeat("big", 450000)})
		return nil
	})
	p.Register(contract.Operation{ID: "huge_stream", Label: "huge stream", Stream: true}, func(ctx plugin.Ctx, raw json.RawMessage, sink plugin.Sink) error {
		sink.Text(strings.Repeat("big", 450000))
		sink.Text("after")
		return nil
	})
	p.Register(contract.Operation{ID: "reserved", Label: "reserved"}, func(ctx plugin.Ctx, raw json.RawMessage, sink plugin.Sink) error {
		sink.Vars(struct {
			Value map[string]any `sokel:"__sokel_payload"`
		}{map[string]any{"version": 1, "subject": "business value"}})
		return nil
	})
	go func() { _ = p.Run() }()
	select {
	case <-pf.regs:
	case <-time.After(5 * time.Second):
		t.Fatal("plugin did not register")
	}
	for i := 0; i < 100; i++ {
		if _, err := pf.nc.Request(subject, []byte(`{"operation":"echo","input":{"text":"ready"}}`), time.Second); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("plugin did not subscribe")
}

func rawReference(t *testing.T, pf *platform, data []byte, firstChunkDelay ...time.Duration) ([]byte, <-chan struct{}) {
	t.Helper()
	transferSubject := "sokel.payload.g1." + strings.TrimPrefix(nats.NewInbox(), "_INBOX.")
	done := make(chan struct{})
	const chunk = 64 << 10
	sub, err := pf.nc.Subscribe(transferSubject, func(m *nats.Msg) {
		var req struct {
			Offset int `json:"offset"`
		}
		_ = json.Unmarshal(m.Data, &req)
		if req.Offset == 0 && len(firstChunkDelay) > 0 {
			time.Sleep(firstChunkDelay[0])
		}
		end := min(req.Offset+chunk, len(data))
		if req.Offset < 0 || req.Offset >= len(data) {
			_ = m.Respond(nil)
			return
		}
		_ = m.Respond(data[req.Offset:end])
		if end == len(data) {
			close(done)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	_ = pf.nc.Flush()
	hash := sha256.Sum256(data)
	body, _ := json.Marshal(map[string]any{"__sokel_payload": map[string]any{"version": 1, "subject": transferSubject, "size": len(data), "sha256": hex.EncodeToString(hash[:]), "chunk_bytes": chunk}})
	return body, done
}

func fetchReference(t *testing.T, nc *nats.Conn, body []byte) []byte {
	t.Helper()
	var ref struct {
		Ref struct {
			Subject string `json:"subject"`
			Size    int    `json:"size"`
			Hash    string `json:"sha256"`
			Chunk   int    `json:"chunk_bytes"`
		} `json:"__sokel_payload"`
	}
	if err := json.Unmarshal(body, &ref); err != nil || ref.Ref.Subject == "" {
		t.Fatalf("expected payload reference, got %.200s (%v)", body, err)
	}
	var out []byte
	for len(out) < ref.Ref.Size {
		msg, err := nc.Request(ref.Ref.Subject, []byte(fmt.Sprintf(`{"offset":%d}`, len(out))), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if len(msg.Data) != min(ref.Ref.Chunk, ref.Ref.Size-len(out)) {
			t.Fatalf("bad chunk length %d", len(msg.Data))
		}
		out = append(out, msg.Data...)
	}
	hash := sha256.Sum256(out)
	if hex.EncodeToString(hash[:]) != ref.Ref.Hash {
		t.Fatal("payload hash mismatch")
	}
	return out
}

func TestPayloadLargeRequestAndReply(t *testing.T) {
	pf := startPlatform(t, 2, 1)
	payloadPlugin(t, pf)
	want := strings.Repeat("行情", 300000)
	raw, _ := json.Marshal(map[string]any{"operation": "echo", "input": map[string]any{"text": want}, "payload_protocol": 1, "deadline_ms": 5000})
	if int64(len(raw)) <= pf.nc.MaxPayload() {
		t.Fatal("test must exceed broker limit")
	}
	ref, done := rawReference(t, pf, raw)
	msg, err := pf.nc.Request(subject+".large", ref, 3*time.Second)
	if err != nil {
		t.Fatalf("large request must reach handler through reference: %v", err)
	}
	if msg.Header.Get("Sokel-Payload") != "1" || msg.Header.Get("Sokel-Instance") == "" {
		t.Fatal("missing reference or replica header")
	}
	body := fetchReference(t, pf.nc, msg.Data)
	var output struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &output); err != nil || output.Text != want {
		t.Fatalf("large response changed: %v", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("input transfer did not complete")
	}
}

func TestPayloadLargeReply(t *testing.T) {
	pf := startPlatform(t, 2, 1)
	payloadPlugin(t, pf)
	msg, err := pf.nc.Request(subject, []byte(`{"operation":"huge","payload_protocol":1,"deadline_ms":5000}`), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("large output must return a reference, not time out: %v", err)
	}
	body := fetchReference(t, pf.nc, msg.Data)
	if !bytes.Contains(body, []byte(strings.Repeat("big", 450000))) {
		t.Fatal("output missing")
	}
}

func TestPayloadLegacyPlatformGetsExplicitError(t *testing.T) {
	pf := startPlatform(t, 2)
	payloadPlugin(t, pf)
	msg, err := pf.nc.Request(subject, []byte(`{"operation":"huge","deadline_ms":5000}`), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("legacy platform needs actionable error, not silent publish failure: %v", err)
	}
	if !bytes.Contains(msg.Data, []byte(`"code":"payload_too_large"`)) {
		t.Fatalf("wrong error: %.300s", msg.Data)
	}
}

func TestPayloadLargeStreamingFrame(t *testing.T) {
	pf := startPlatform(t, 2, 1)
	payloadPlugin(t, pf)
	inbox := nats.NewInbox()
	sub, err := pf.nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	_ = pf.nc.PublishRequest(subject, inbox, []byte(`{"operation":"huge_stream","payload_protocol":1,"deadline_ms":5000}`))
	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Sokel-Payload") != "1" {
		t.Fatal("missing stream reference marker")
	}
	body := fetchReference(t, pf.nc, msg.Data)
	if !bytes.Contains(body, []byte(strings.Repeat("big", 450000))) {
		t.Fatal("stream output missing")
	}
	for _, want := range []string{`"text":"after"`, `"kind":"end"`} {
		msg, err = sub.NextMsg(time.Second)
		if err != nil || !bytes.Contains(msg.Data, []byte(want)) {
			t.Fatalf("expected %s after large frame: %v", want, err)
		}
	}
}

func TestPayloadBusinessReservedKeyRemainsOrdinary(t *testing.T) {
	pf := startPlatform(t, 2, 1)
	payloadPlugin(t, pf)
	msg, err := pf.nc.Request(subject, []byte(`{"operation":"reserved","payload_protocol":1}`), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Sokel-Payload") != "" || !bytes.Contains(msg.Data, []byte(`"subject":"business value"`)) {
		t.Fatalf("business output became transport reference: %s", msg.Data)
	}
}

func payloadNATS(t *testing.T) *nats.Conn {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, MaxPayload: 32 << 10})
	if err != nil {
		t.Fatal(err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(time.Second) {
		t.Fatal("NATS not ready")
	}
	t.Cleanup(ns.Shutdown)
	nc, err := nats.Connect(ns.ClientURL(), nats.CustomInboxPrefix("_INBOX_G.g1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func requestPayload(nc *nats.Conn) payload.RequestFunc {
	return func(ctx context.Context, subject string, body []byte) ([]byte, error) {
		msg, err := nc.RequestWithContext(ctx, subject, body)
		if err != nil {
			return nil, err
		}
		if msg.Header.Get("Status") == "400" {
			return nil, errors.New("invalid chunk request")
		}
		return msg.Data, nil
	}
}

func referenceSubject(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Ref struct {
			Subject string `json:"subject"`
		} `json:"__sokel_payload"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	return env.Ref.Subject
}

func assertTransferReleased(t *testing.T, nc *nats.Conn, body []byte) {
	t.Helper()
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	_, err := nc.Request(referenceSubject(t, body), []byte(`{"offset":0}`), time.Second)
	if !errors.Is(err, nats.ErrNoResponders) {
		t.Fatalf("transfer subscription remains: %v", err)
	}
}

func TestPayloadProducerRoundTripAndLifecycle(t *testing.T) {
	nc := payloadNATS(t)
	want := bytes.Repeat([]byte("large content"), 12000)
	const prefix = "sokel.payload.g1."
	body, transfer, err := payload.Prepare(context.Background(), nc, prefix, want)
	if err != nil || transfer == nil {
		t.Fatalf("prepare: %v", err)
	}
	defer transfer.Close()
	got, err := payload.Fetch(context.Background(), body, prefix, requestPayload(nc))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("round trip: %v", err)
	}
	if err := transfer.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-transfer.Done():
	default:
		t.Fatal("done not closed")
	}
	assertTransferReleased(t, nc, body)
	inline, tr, err := payload.Prepare(context.Background(), nc, prefix, []byte("small"))
	if err != nil || tr != nil || string(inline) != "small" {
		t.Fatalf("inline: %s %v", inline, err)
	}
	if _, _, err := payload.Prepare(context.Background(), nc, "other.group.", want); err == nil {
		t.Fatal("accepted invalid producer prefix")
	}

	for _, mode := range []string{"close", "cancel", "deadline", "consumer_cancel", "bad_offset", "fractional_offset", "missing_offset", "trailing_json"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
			}
			defer cancel()
			body, tr, err := payload.Prepare(ctx, nc, prefix, want)
			if err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			switch mode {
			case "close":
				tr.Close()
				tr.Close()
			case "cancel":
				cancel()
			case "consumer_cancel":
				fetchCtx, fetchCancel := context.WithCancel(context.Background())
				fetchCancel()
				if _, err := payload.Fetch(fetchCtx, body, prefix, requestPayload(nc)); !errors.Is(err, context.Canceled) {
					t.Fatalf("fetch did not cancel: %v", err)
				}
			case "bad_offset", "fractional_offset", "missing_offset", "trailing_json":
				requests := map[string]string{"bad_offset": `{"offset":-1}`, "fractional_offset": `{"offset":0.5}`, "missing_offset": `{}`, "trailing_json": `{"offset":0} garbage`}
				if _, err := requestPayload(nc)(ctx, referenceSubject(t, body), []byte(requests[mode])); err == nil {
					t.Fatal("invalid offset succeeded")
				}
			}
			if err := tr.Wait(); err == nil {
				t.Fatal("failed transfer reported success")
			}
			assertTransferReleased(t, nc, body)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := payload.Prepare(ctx, nc, prefix, want); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prepare: %v", err)
	}
}

func TestPayloadLegacyStreamingReportsError(t *testing.T) {
	pf := startPlatform(t, 2)
	payloadPlugin(t, pf)
	inbox := nats.NewInbox()
	sub, err := pf.nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	_ = pf.nc.PublishRequest(subject, inbox, []byte(`{"operation":"huge_stream"}`))
	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(msg.Data, []byte(`"code":"payload_too_large"`)) {
		t.Fatalf("missing stream error: %s", msg.Data)
	}
	msg, err = sub.NextMsg(time.Second)
	if err != nil || !bytes.Contains(msg.Data, []byte(`"kind":"end"`)) {
		t.Fatalf("missing stream terminator: %v", err)
	}
}

func TestPayloadRequestDownloadConsumesDeadline(t *testing.T) {
	pf := startPlatform(t, 2, 1)
	invoked := make(chan struct{}, 1)
	payloadPlugin(t, pf, func() { invoked <- struct{}{} })
	raw, _ := json.Marshal(map[string]any{"operation": "echo", "input": map[string]any{"text": strings.Repeat("big", 450000)}, "payload_protocol": 1, "deadline_ms": 10})
	ref, _ := rawReference(t, pf, raw, 50*time.Millisecond)
	msg, err := pf.nc.Request(subject+".large", ref, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(msg.Data, []byte(`"code":"retryable"`)) {
		t.Fatalf("expired request was invoked: %.300s", msg.Data)
	}
	select {
	case <-invoked:
		t.Fatal("handler ran after its deadline")
	default:
	}
}
