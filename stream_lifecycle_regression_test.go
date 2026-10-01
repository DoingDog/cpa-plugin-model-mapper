package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestFunctionalShutdownUnblocksEmit(t *testing.T) {
	testFunctionalShutdownUnblocksEmit(t, false)
}

func TestFunctionalShutdownUnblocksFinalFlush(t *testing.T) {
	testFunctionalShutdownUnblocksEmit(t, true)
}

func testFunctionalShutdownUnblocksEmit(t *testing.T, finalFlush bool) {
	t.Helper()
	resetExecutorStreamLifecycle()
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	queue := make(chan []byte, 16)
	blocked := make(chan struct{})
	downstreamClosed := make(chan struct{})
	hostClosed := make(chan struct{})
	shutdownDone := make(chan struct{})
	terminal := make(chan string, 1)
	var signalBlocked, closeDownstream, closeHost sync.Once
	var hostCalls, pluginCalls atomic.Int32
	shutdownLaunched := false
	startShutdown := func() {
		if shutdownLaunched {
			return
		}
		shutdownLaunched = true
		go func() { shutdownExecutorStreams(); close(shutdownDone) }()
	}
	t.Cleanup(func() {
		closeDownstream.Do(func() { close(downstreamClosed) })
		closeHost.Do(func() { close(hostClosed) })
		startShutdown()
		select {
		case <-shutdownDone:
		case <-time.After(2 * time.Second):
			t.Error("original shutdown did not return after callback release")
			return
		}
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	reads := 0
	call := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			contentType := "application/json"
			if finalFlush {
				contentType = "text/event-stream"
			}
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-full", Headers: map[string][]string{"Content-Type": {contentType}}})
		case pluginabi.MethodHostModelStreamRead:
			select {
			case <-hostClosed:
				return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true})
			default:
				reads++
				if finalFlush {
					if reads <= cap(queue) {
						return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte("data: {\"model\":\"upstream\"}\n\n")})
					}
					if reads == cap(queue)+1 {
						return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte(`data: {"model":"upstream"}`)})
					}
					return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true})
				}
				return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte(`{"model":"upstream"}`)})
			}
		case pluginabi.MethodHostStreamEmit:
			if len(queue) == cap(queue) {
				signalBlocked.Do(func() { close(blocked) })
			}
			select {
			case queue <- []byte("complete chunk"):
				return json.RawMessage(`{}`), nil
			case <-downstreamClosed:
				return nil, fmt.Errorf("downstream closed during shutdown")
			}
		case pluginabi.MethodHostModelStreamClose:
			hostCalls.Add(1)
			closeHost.Do(func() { close(hostClosed) })
			return json.RawMessage(`{}`), nil
		case pluginabi.MethodHostStreamClose:
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var closePayload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(raw, &closePayload); err != nil {
				return nil, err
			}
			pluginCalls.Add(1)
			closeDownstream.Do(func() { terminal <- closePayload.Error; close(downstreamClosed) })
			return json.RawMessage(`{}`), nil
		default:
			return nil, fmt.Errorf("unexpected callback %s", method)
		}
	}
	if _, err := handleExecutorExecuteStream(executorStreamLifecycleRequest(t, "plugin-full"), call); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("emit did not reach full queue")
	}
	if finalFlush && reads != cap(queue)+2 {
		t.Fatalf("blocked after %d reads, want final Done read after %d complete chunks and pending tail", reads, cap(queue))
	}
	startShutdown()
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown waits on emit")
	}
	select {
	case errText := <-terminal:
		if strings.TrimSpace(errText) == "" {
			t.Fatal("forced shutdown reported clean completion")
		}
	default:
		t.Fatal("terminal close missing")
	}
	if hostCalls.Load() != 1 || pluginCalls.Load() != 1 {
		t.Fatalf("closes host=%d plugin=%d", hostCalls.Load(), pluginCalls.Load())
	}
}

func TestFunctionalShutdownTerminalOrders(t *testing.T) {
	for _, naturalFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("natural-first=%v", naturalFirst), func(t *testing.T) {
			resetExecutorStreamLifecycle()
			setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
			ready := make(chan struct{})
			hostClosed := make(chan struct{})
			releaseCleanup := make(chan struct{})
			shutdownDone := make(chan struct{})
			terminal := make(chan string, 1)
			var readyOnce, hostOnce, releaseOnce, terminalOnce sync.Once
			var hostCalls, pluginCalls atomic.Int32
			var emitted string
			var emitMu sync.Mutex
			reads := 0
			shutdownLaunched := false
			startShutdown := func() {
				if shutdownLaunched {
					return
				}
				shutdownLaunched = true
				go func() { shutdownExecutorStreams(); close(shutdownDone) }()
			}
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(releaseCleanup) })
				hostOnce.Do(func() { close(hostClosed) })
				startShutdown()
				select {
				case <-shutdownDone:
				case <-time.After(2 * time.Second):
					t.Error("original shutdown did not return after callback release")
					return
				}
				resetExecutorStreamLifecycle()
				setLoadedConfigForTest(defaultConfig())
			})
			call := func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostModelExecuteStream:
					return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-order", Headers: map[string][]string{"Content-Type": {"application/json"}}})
				case pluginabi.MethodHostModelStreamRead:
					reads++
					if reads == 1 {
						return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte(`{"model":"upstream","text":"partial"}`)})
					}
					if !naturalFirst {
						readyOnce.Do(func() { close(ready) })
						<-hostClosed
					}
					return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true})
				case pluginabi.MethodHostStreamEmit:
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var emit struct {
						Payload []byte `json:"payload"`
					}
					if err := json.Unmarshal(raw, &emit); err != nil {
						return nil, err
					}
					emitMu.Lock()
					emitted += string(emit.Payload)
					emitMu.Unlock()
					return json.RawMessage(`{}`), nil
				case pluginabi.MethodHostModelStreamClose:
					hostCalls.Add(1)
					if naturalFirst {
						readyOnce.Do(func() { close(ready) })
						<-releaseCleanup
					}
					hostOnce.Do(func() { close(hostClosed) })
					return json.RawMessage(`{}`), nil
				case pluginabi.MethodHostStreamClose:
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var closePayload struct {
						Error string `json:"error"`
					}
					if err := json.Unmarshal(raw, &closePayload); err != nil {
						return nil, err
					}
					pluginCalls.Add(1)
					terminalOnce.Do(func() { terminal <- closePayload.Error })
					return json.RawMessage(`{}`), nil
				default:
					return nil, fmt.Errorf("unexpected callback %s", method)
				}
			}
			if _, err := handleExecutorExecuteStream(executorStreamLifecycleRequest(t, "plugin-order"), call); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(2 * time.Second):
				t.Fatal("worker did not reach ordered point")
			}
			startShutdown()
			if naturalFirst {
				deadline := time.After(2 * time.Second)
				for {
					executorStreamLifecycle.mu.Lock()
					stopping := executorStreamLifecycle.stopping
					executorStreamLifecycle.mu.Unlock()
					if stopping {
						break
					}
					select {
					case <-deadline:
						t.Fatal("shutdown did not mark executor streams stopping")
					default:
						runtime.Gosched()
					}
				}
				select {
				case <-shutdownDone:
					t.Fatal("shutdown returned while cleanup waits")
				default:
				}
				releaseOnce.Do(func() { close(releaseCleanup) })
			}
			select {
			case <-shutdownDone:
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown did not complete")
			}
			select {
			case errText := <-terminal:
				if naturalFirst && errText != "" {
					t.Fatalf("natural completion error=%q", errText)
				}
				if !naturalFirst && strings.TrimSpace(errText) == "" {
					t.Fatal("interruption reported clean completion")
				}
			default:
				t.Fatal("terminal close missing")
			}
			emitMu.Lock()
			got := emitted
			emitMu.Unlock()
			if !strings.Contains(got, `"model":"client"`) || strings.Contains(got, "[DONE]") {
				t.Fatalf("output=%q", got)
			}
			if hostCalls.Load() != 1 || pluginCalls.Load() != 1 {
				t.Fatalf("closes host=%d plugin=%d", hostCalls.Load(), pluginCalls.Load())
			}
		})
	}
}

func TestFunctionalShutdownBetweenReads(t *testing.T) {
	resetExecutorStreamLifecycle()
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	ready := make(chan struct{})
	releaseEmit := make(chan struct{})
	hostClosed := make(chan struct{})
	shutdownDone := make(chan struct{})
	terminal := make(chan string, 1)
	var readyOnce, releaseOnce, hostOnce, terminalOnce sync.Once
	var reads, hostCalls, pluginCalls atomic.Int32
	var emitted []byte
	input := []byte(`{"model":"upstream","text":"upstream","tool":{"model":"upstream"}}`)
	originalInput := bytes.Clone(input)
	shutdownLaunched := false
	startShutdown := func() {
		if shutdownLaunched {
			return
		}
		shutdownLaunched = true
		go func() { shutdownExecutorStreams(); close(shutdownDone) }()
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseEmit) })
		hostOnce.Do(func() { close(hostClosed) })
		startShutdown()
		select {
		case <-shutdownDone:
		case <-time.After(2 * time.Second):
			t.Error("original shutdown did not return after callback release")
			return
		}
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	call := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-between", Headers: map[string][]string{"Content-Type": {"application/json"}}})
		case pluginabi.MethodHostModelStreamRead:
			if reads.Add(1) == 1 {
				return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: input})
			}
			return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true})
		case pluginabi.MethodHostStreamEmit:
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var emit struct {
				Payload []byte `json:"payload"`
			}
			if err := json.Unmarshal(raw, &emit); err != nil {
				return nil, err
			}
			emitted = append(emitted, emit.Payload...)
			readyOnce.Do(func() { close(ready) })
			<-releaseEmit
			return json.RawMessage(`{}`), nil
		case pluginabi.MethodHostModelStreamClose:
			hostCalls.Add(1)
			hostOnce.Do(func() { close(hostClosed) })
			return json.RawMessage(`{}`), nil
		case pluginabi.MethodHostStreamClose:
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var closePayload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(raw, &closePayload); err != nil {
				return nil, err
			}
			pluginCalls.Add(1)
			terminalOnce.Do(func() { terminal <- closePayload.Error })
			return json.RawMessage(`{}`), nil
		default:
			return nil, fmt.Errorf("unexpected callback %s", method)
		}
	}
	if _, err := handleExecutorExecuteStream(executorStreamLifecycleRequest(t, "plugin-between"), call); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not reach emit between reads")
	}
	startShutdown()
	select {
	case <-hostClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not close host stream")
	}
	releaseOnce.Do(func() { close(releaseEmit) })
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	if reads.Load() != 1 {
		t.Fatalf("reads after interruption=%d, want no second read", reads.Load())
	}
	select {
	case errText := <-terminal:
		if strings.TrimSpace(errText) == "" {
			t.Fatal("interruption between reads reported clean completion")
		}
	default:
		t.Fatal("terminal close missing")
	}
	if got, want := string(emitted), `{"model":"client","text":"upstream","tool":{"model":"upstream"}}`; got != want {
		t.Fatalf("emitted=%q, want %q", got, want)
	}
	if !bytes.Equal(input, originalInput) {
		t.Fatalf("input changed to %q", input)
	}
	if hostCalls.Load() != 1 || pluginCalls.Load() != 1 {
		t.Fatalf("closes host=%d plugin=%d", hostCalls.Load(), pluginCalls.Load())
	}
}

func TestFunctionalShutdownPreservesPrimaryErrors(t *testing.T) {
	for _, inBand := range []bool{false, true} {
		t.Run(fmt.Sprintf("in-band=%v", inBand), func(t *testing.T) {
			testFunctionalShutdownPreservesErrors(t, inBand, true, true)
		})
	}
}

func TestFunctionalShutdownPreservesCleanupErrors(t *testing.T) {
	for _, inBand := range []bool{false, true} {
		for _, overlap := range []bool{false, true} {
			t.Run(fmt.Sprintf("in-band=%v/overlap=%v", inBand, overlap), func(t *testing.T) {
				testFunctionalShutdownPreservesErrors(t, inBand, false, overlap)
			})
		}
	}
}

func waitFunctionalShutdownHostCleanup(t *testing.T) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	stackBuffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(stackBuffer, true)
		if n == len(stackBuffer) {
			t.Fatal("goroutine snapshot is truncated")
		}
		for _, stack := range bytes.Split(stackBuffer[:n], []byte("\n\n")) {
			// 确认 shutdown 已经过提前关闭阶段，正在等待本测试尚未释放的 host cleanup。
			if bytes.Contains(stack, []byte("shutdownExecutorStreams(")) && bytes.Contains(stack, []byte("(*executorStream).closeHost(")) {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatal("shutdown did not reach host cleanup wait")
		default:
			runtime.Gosched()
		}
	}
}

func testFunctionalShutdownPreservesErrors(t *testing.T, inBand, directFails, overlap bool) {
	t.Helper()
	resetExecutorStreamLifecycle()
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	sentinelRead := errors.New("host stream read failed")
	sentinelHostClose := errors.New("host close failed")
	sentinelPluginClose := errors.New("plugin close failed")
	ready := make(chan struct{})
	releaseCleanup := make(chan struct{})
	shutdownDone := make(chan struct{})
	terminal := make(chan string, 2)
	var readyOnce, releaseOnce sync.Once
	var hostCalls, directCalls, outerCalls, successfulCloses atomic.Int32
	reads := 0
	var emitted []byte
	shutdownLaunched := false
	startShutdown := func() {
		if shutdownLaunched {
			return
		}
		shutdownLaunched = true
		go func() { shutdownExecutorStreams(); close(shutdownDone) }()
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCleanup) })
		startShutdown()
		select {
		case <-shutdownDone:
		case <-time.After(2 * time.Second):
			t.Error("original shutdown did not return after callback release")
			return
		}
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	call := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-primary", Headers: map[string][]string{"Content-Type": {"application/json"}}})
		case pluginabi.MethodHostModelStreamRead:
			reads++
			if reads == 1 {
				return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte(`{"model":"upstream","text":"upstream","tool":{"model":"upstream"}}`)})
			}
			if inBand {
				return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true, Error: "primary upstream error"})
			}
			return nil, sentinelRead
		case pluginabi.MethodHostStreamEmit:
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var emit struct {
				Payload []byte `json:"payload"`
			}
			if err := json.Unmarshal(raw, &emit); err != nil {
				return nil, err
			}
			emitted = append(emitted, emit.Payload...)
			return json.RawMessage(`{}`), nil
		case pluginabi.MethodHostModelStreamClose:
			hostCalls.Add(1)
			readyOnce.Do(func() { close(ready) })
			<-releaseCleanup
			return nil, sentinelHostClose
		case pluginabi.MethodHostStreamClose:
			directCalls.Add(1)
			if directFails {
				return nil, sentinelPluginClose
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var closePayload struct {
				StreamID string `json:"stream_id"`
				Error    string `json:"error"`
			}
			if err := json.Unmarshal(raw, &closePayload); err != nil {
				return nil, err
			}
			if closePayload.StreamID != "plugin-primary" {
				return nil, fmt.Errorf("closed plugin stream %q", closePayload.StreamID)
			}
			successfulCloses.Add(1)
			terminal <- closePayload.Error
			return json.RawMessage(`{}`), nil
		default:
			return nil, fmt.Errorf("unexpected callback %s", method)
		}
	}
	var req executorRPCRequest
	if err := json.Unmarshal(executorStreamLifecycleRequest(t, "plugin-primary"), &req); err != nil {
		t.Fatal(err)
	}
	if _, err := startExecutorStream(req, call, func(streamID, errText string) error {
		outerCalls.Add(1)
		if streamID != "plugin-primary" {
			return fmt.Errorf("outer closed plugin stream %q", streamID)
		}
		successfulCloses.Add(1)
		terminal <- errText
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("primary error did not reach host cleanup")
	}
	if overlap {
		startShutdown()
		waitFunctionalShutdownHostCleanup(t)
	}
	releaseOnce.Do(func() { close(releaseCleanup) })
	var got string
	select {
	case got = <-terminal:
	case <-time.After(2 * time.Second):
		t.Fatal("successful terminal close missing")
	}
	startShutdown()
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	wantPrimary := "read host stream: " + sentinelRead.Error()
	if inBand {
		wantPrimary = "primary upstream error"
	}
	if !strings.HasPrefix(got, wantPrimary) {
		t.Errorf("terminal error=%q, want primary %q first", got, wantPrimary)
	}
	wantCleanup := []string{sentinelHostClose.Error()}
	if directFails && directCalls.Load() != 0 {
		wantCleanup = append(wantCleanup, sentinelPluginClose.Error())
	}
	for _, want := range wantCleanup {
		if !strings.Contains(got, want) {
			t.Errorf("terminal error=%q, missing %q", got, want)
		}
	}
	if strings.Contains(got, errExecutorStreamShutdown.Error()) {
		t.Errorf("known primary replaced by interruption: %q", got)
	}
	if reads != 2 || string(emitted) != `{"model":"client","text":"upstream","tool":{"model":"upstream"}}` {
		t.Errorf("reads=%d output=%q", reads, emitted)
	}
	if hostCalls.Load() != 1 || directCalls.Load() > 1 || successfulCloses.Load() != 1 || len(terminal) != 0 {
		t.Errorf("closes host=%d direct=%d outer=%d successful=%d extra terminal=%d", hostCalls.Load(), directCalls.Load(), outerCalls.Load(), successfulCloses.Load(), len(terminal))
	}
	if directFails {
		if outerCalls.Load() != 1 || (inBand && directCalls.Load() != 1) {
			t.Errorf("remedial closes direct=%d outer=%d", directCalls.Load(), outerCalls.Load())
		}
	} else if inBand {
		if directCalls.Load() != 1 || outerCalls.Load() != 0 {
			t.Errorf("in-band closes direct=%d outer=%d", directCalls.Load(), outerCalls.Load())
		}
	} else if directCalls.Load() != 0 || outerCalls.Load() != 1 {
		t.Errorf("read error closes direct=%d outer=%d", directCalls.Load(), outerCalls.Load())
	}
}

func TestFunctionalShutdownPreservesInBandErrorAtBackpressure(t *testing.T) {
	for _, directFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct-fails=%v", directFails), func(t *testing.T) {
			resetExecutorStreamLifecycle()
			setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
			queue := make(chan []byte, 16)
			blocked := make(chan []byte, 1)
			downstreamClosed := make(chan struct{})
			shutdownDone := make(chan struct{})
			terminal := make(chan string, 2)
			sentinelEmit := errors.New("downstream closed during shutdown")
			sentinelHostClose := errors.New("host close failed")
			sentinelPluginClose := errors.New("plugin close failed")
			var downstreamOnce sync.Once
			var reads, emits, hostCalls, directCalls, outerCalls, successfulCloses atomic.Int32
			shutdownLaunched := false
			startShutdown := func() {
				if shutdownLaunched {
					return
				}
				shutdownLaunched = true
				go func() { shutdownExecutorStreams(); close(shutdownDone) }()
			}
			t.Cleanup(func() {
				downstreamOnce.Do(func() { close(downstreamClosed) })
				startShutdown()
				select {
				case <-shutdownDone:
				case <-time.After(2 * time.Second):
					t.Error("original shutdown did not return after callback release")
					return
				}
				resetExecutorStreamLifecycle()
				setLoadedConfigForTest(defaultConfig())
			})
			call := func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostModelExecuteStream:
					return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-known-error", Headers: map[string][]string{"Content-Type": {"application/json"}}})
				case pluginabi.MethodHostModelStreamRead:
					n := reads.Add(1)
					chunk := pluginapi.HostModelStreamReadResponse{Payload: []byte(fmt.Sprintf(`{"index":%d,"model":"upstream","text":"upstream","tool":{"model":"upstream"}}`, n))}
					if n == int32(cap(queue)+1) {
						chunk.Done = true
						chunk.Error = "primary upstream error"
					}
					return json.Marshal(chunk)
				case pluginabi.MethodHostStreamEmit:
					emits.Add(1)
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var emit struct {
						StreamID string `json:"stream_id"`
						Payload  []byte `json:"payload"`
					}
					if err := json.Unmarshal(raw, &emit); err != nil {
						return nil, err
					}
					if emit.StreamID != "plugin-known-error" {
						return nil, fmt.Errorf("emit stream %q", emit.StreamID)
					}
					if len(queue) == cap(queue) {
						blocked <- emit.Payload
					}
					select {
					case queue <- emit.Payload:
						return json.RawMessage(`{}`), nil
					case <-downstreamClosed:
						return nil, sentinelEmit
					}
				case pluginabi.MethodHostModelStreamClose:
					hostCalls.Add(1)
					if payload.(pluginapi.HostModelStreamCloseRequest).StreamID != "host-known-error" {
						return nil, fmt.Errorf("unexpected host close payload %v", payload)
					}
					if directFails {
						return nil, sentinelHostClose
					}
					return json.RawMessage(`{}`), nil
				case pluginabi.MethodHostStreamClose:
					directCalls.Add(1)
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var closePayload struct {
						StreamID string `json:"stream_id"`
						Error    string `json:"error"`
					}
					if err := json.Unmarshal(raw, &closePayload); err != nil {
						return nil, err
					}
					if closePayload.StreamID != "plugin-known-error" {
						return nil, fmt.Errorf("closed plugin stream %q", closePayload.StreamID)
					}
					downstreamOnce.Do(func() { close(downstreamClosed) })
					if directFails {
						return nil, sentinelPluginClose
					}
					successfulCloses.Add(1)
					terminal <- closePayload.Error
					return json.RawMessage(`{}`), nil
				default:
					return nil, fmt.Errorf("unexpected callback %s", method)
				}
			}
			var req executorRPCRequest
			if err := json.Unmarshal(executorStreamLifecycleRequest(t, "plugin-known-error"), &req); err != nil {
				t.Fatal(err)
			}
			if _, err := startExecutorStream(req, call, func(streamID, errText string) error {
				outerCalls.Add(1)
				if streamID != "plugin-known-error" {
					return fmt.Errorf("outer closed plugin stream %q", streamID)
				}
				successfulCloses.Add(1)
				terminal <- errText
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case pending := <-blocked:
				if got, want := string(pending), `{"index":17,"model":"client","text":"upstream","tool":{"model":"upstream"}}`; got != want {
					t.Fatalf("terminal payload=%q, want %q", got, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("terminal payload emit did not reach full queue")
			}
			if reads.Load() != 17 || emits.Load() != 17 || len(queue) != cap(queue) {
				t.Fatalf("blocked reads=%d emits=%d queued=%d", reads.Load(), emits.Load(), len(queue))
			}
			startShutdown()
			select {
			case <-shutdownDone:
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown waits on terminal payload emit")
			}
			select {
			case got := <-terminal:
				if !strings.HasPrefix(got, "primary upstream error") || strings.Contains(got, errExecutorStreamShutdown.Error()) {
					t.Errorf("terminal error=%q, want known upstream primary without generic interruption", got)
				}
				if directFails {
					for _, want := range []string{sentinelEmit.Error(), sentinelHostClose.Error(), sentinelPluginClose.Error()} {
						if !strings.Contains(got, want) {
							t.Errorf("outer terminal error=%q, missing %q", got, want)
						}
					}
				}
			default:
				t.Error("successful terminal close missing")
			}
			wantOuter := int32(0)
			if directFails {
				wantOuter = 1
			}
			if reads.Load() != 17 || emits.Load() != 17 || hostCalls.Load() != 1 || directCalls.Load() != 1 || outerCalls.Load() != wantOuter || successfulCloses.Load() != 1 || len(terminal) != 0 {
				t.Errorf("reads=%d emits=%d closes host=%d direct=%d outer=%d successful=%d extra terminal=%d", reads.Load(), emits.Load(), hostCalls.Load(), directCalls.Load(), outerCalls.Load(), successfulCloses.Load(), len(terminal))
			}
			for n := 1; n <= cap(queue); n++ {
				if got, want := string(<-queue), fmt.Sprintf(`{"index":%d,"model":"client","text":"upstream","tool":{"model":"upstream"}}`, n); got != want {
					t.Errorf("accepted chunk %d=%q, want %q", n, got, want)
				}
			}
		})
	}
}

func TestFunctionalShutdownConcurrentClosesOnce(t *testing.T) {
	resetExecutorStreamLifecycle()
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	ready := make(chan struct{})
	hostClosed := make(chan struct{})
	releaseClose := make(chan struct{})
	shutdownDone := make(chan struct{})
	terminal := make(chan string, 1)
	var readyOnce, hostOnce, releaseOnce, terminalOnce sync.Once
	var hostCalls, pluginCalls atomic.Int32
	shutdownLaunched := false
	startShutdown := func() {
		if shutdownLaunched {
			return
		}
		shutdownLaunched = true
		var wg sync.WaitGroup
		wg.Add(4)
		for range 4 {
			go func() { defer wg.Done(); shutdownExecutorStreams() }()
		}
		go func() { wg.Wait(); close(shutdownDone) }()
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseClose) })
		hostOnce.Do(func() { close(hostClosed) })
		startShutdown()
		select {
		case <-shutdownDone:
		case <-time.After(2 * time.Second):
			t.Error("original shutdowns did not return after callback release")
			return
		}
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	call := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-concurrent", Headers: map[string][]string{"Content-Type": {"application/json"}}})
		case pluginabi.MethodHostModelStreamRead:
			readyOnce.Do(func() { close(ready) })
			<-hostClosed
			return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true})
		case pluginabi.MethodHostModelStreamClose:
			hostCalls.Add(1)
			hostOnce.Do(func() { close(hostClosed) })
			return json.RawMessage(`{}`), nil
		case pluginabi.MethodHostStreamClose:
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var closePayload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(raw, &closePayload); err != nil {
				return nil, err
			}
			pluginCalls.Add(1)
			terminalOnce.Do(func() { terminal <- closePayload.Error })
			<-releaseClose
			return json.RawMessage(`{}`), nil
		default:
			return nil, fmt.Errorf("unexpected callback %s", method)
		}
	}
	if _, err := handleExecutorExecuteStream(executorStreamLifecycleRequest(t, "plugin-concurrent"), call); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start read")
	}
	startShutdown()
	deadline := time.After(2 * time.Second)
	for {
		executorStreamLifecycle.mu.Lock()
		shutdowns := executorStreamLifecycle.shutdowns
		executorStreamLifecycle.mu.Unlock()
		if shutdowns == 4 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("concurrent shutdowns did not enter lifecycle")
		default:
			runtime.Gosched()
		}
	}
	select {
	case <-shutdownDone:
		t.Fatal("shutdowns returned before terminal callback release")
	default:
	}
	releaseOnce.Do(func() { close(releaseClose) })
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdowns did not complete")
	}
	select {
	case errText := <-terminal:
		if strings.TrimSpace(errText) == "" {
			t.Fatal("concurrent interruption reported clean completion")
		}
	default:
		t.Fatal("terminal close missing")
	}
	if hostCalls.Load() != 1 || pluginCalls.Load() != 1 {
		t.Fatalf("closes host=%d plugin=%d", hostCalls.Load(), pluginCalls.Load())
	}
}

func BenchmarkFunctionalRunStreamForward(b *testing.B) {
	const chunks = 128
	readRaw, err := json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte(`{"model":"upstream","text":"opaque upstream","tool":{"model":"upstream"}}`)})
	if err != nil {
		b.Fatal(err)
	}
	doneRaw := json.RawMessage(`{"done":true}`)
	reads, emits, hostCloses, pluginCloses := 0, 0, 0, 0
	preflight := true
	call := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelStreamRead:
			reads++
			if reads <= chunks {
				return readRaw, nil
			}
			return doneRaw, nil
		case pluginabi.MethodHostStreamEmit:
			emits++
			if preflight {
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				var emit struct {
					Payload []byte `json:"payload"`
				}
				if err := json.Unmarshal(raw, &emit); err != nil {
					return nil, err
				}
				if got, want := string(emit.Payload), `{"model":"client","text":"opaque upstream","tool":{"model":"upstream"}}`; got != want {
					return nil, fmt.Errorf("output=%q, want %q", got, want)
				}
			}
		case pluginabi.MethodHostModelStreamClose:
			hostCloses++
		case pluginabi.MethodHostStreamClose:
			pluginCloses++
		default:
			return nil, fmt.Errorf("unexpected callback %s", method)
		}
		return json.RawMessage(`{}`), nil
	}
	stream := &executorStream{pluginStreamID: "plugin-bench", hostStreamID: "host-bench", originalModel: "client", format: "openai", call: call}
	if err := runStreamForward(stream); err != nil {
		b.Fatal(err)
	}
	if reads != chunks+1 || emits != chunks || hostCloses != 1 || pluginCloses != 1 {
		b.Fatalf("preflight reads=%d emits=%d host-closes=%d plugin-closes=%d", reads, emits, hostCloses, pluginCloses)
	}
	preflight = false
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		reads, emits, hostCloses, pluginCloses = 0, 0, 0, 0
		stream := &executorStream{pluginStreamID: "plugin-bench", hostStreamID: "host-bench", originalModel: "client", format: "openai", call: call}
		if err := runStreamForward(stream); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFunctionalShutdownLifecycleReset(t *testing.T) {
	executorStreamLifecycle.mu.Lock()
	defer executorStreamLifecycle.mu.Unlock()
	if executorStreamLifecycle.stopping || executorStreamLifecycle.preparing != 0 || executorStreamLifecycle.shutdowns != 0 || len(executorStreamLifecycle.active) != 0 {
		t.Fatalf("lifecycle after teardown: stopping=%v preparing=%d shutdowns=%d active=%d", executorStreamLifecycle.stopping, executorStreamLifecycle.preparing, executorStreamLifecycle.shutdowns, len(executorStreamLifecycle.active))
	}
}
