package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"sync"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func isEventStreamContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "text/event-stream")
}

func releaseExecutorStreamSetup(req *executorRPCRequest) {
	if req == nil {
		return
	}
	req.OriginalRequest = nil
	req.Headers = nil
	req.Query = nil
	req.Metadata = nil
}

func emitRewritten(chunks [][]byte, batch bool, emit func([]byte) error) error {
	if !batch {
		for _, chunk := range chunks {
			if len(chunk) > 0 {
				if err := emit(chunk); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if len(chunks) == 1 {
		if len(chunks[0]) == 0 {
			return nil
		}
		return emit(chunks[0])
	}
	total := 0
	for _, chunk := range chunks {
		total += len(chunk)
	}
	if total == 0 {
		return nil
	}
	batchPayload := make([]byte, 0, total)
	for _, chunk := range chunks {
		batchPayload = append(batchPayload, chunk...)
	}
	return emit(batchPayload)
}

func handleExecutorExecuteStream(raw []byte, call hostCaller) ([]byte, error) {
	var req executorRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	canonicalizeHeaders(req.Headers)
	if req.StreamID == "" {
		return nil, fmt.Errorf("missing plugin stream id")
	}
	return startExecutorStream(req, call, func(streamID, errText string) error {
		_, err := call(pluginabi.MethodHostStreamClose, struct {
			StreamID string `json:"stream_id"`
			Error    string `json:"error,omitempty"`
		}{StreamID: streamID, Error: errText})
		return err
	})
}

type executorStream struct {
	pluginStreamID    string
	hostStreamID      string
	originalModel     string
	format            string
	frameRawJSONAsSSE bool
	call              hostCaller
	closeHostOnce     sync.Once
	closeHostErr      error
	closePluginOnce   sync.Once
	closePluginErr    error
	terminalSelected  bool
	terminalErr       error
	outputFinished    bool
}

var errExecutorStreamShutdown = errors.New("executor stream interrupted during shutdown")

var executorStreamLifecycle = struct {
	mu        sync.Mutex
	stopping  bool
	preparing int
	shutdowns int
	active    map[*executorStream]struct{}
	wg        sync.WaitGroup
}{active: make(map[*executorStream]struct{})}

func resetExecutorStreamLifecycle() {
	executorStreamLifecycle.mu.Lock()
	defer executorStreamLifecycle.mu.Unlock()
	if len(executorStreamLifecycle.active) != 0 || executorStreamLifecycle.preparing != 0 || executorStreamLifecycle.shutdowns != 0 {
		return
	}
	executorStreamLifecycle.stopping = false
	executorStreamLifecycle.active = make(map[*executorStream]struct{})
}

func beginExecutorStreamPreparation() bool {
	executorStreamLifecycle.mu.Lock()
	defer executorStreamLifecycle.mu.Unlock()
	if executorStreamLifecycle.stopping {
		return false
	}
	executorStreamLifecycle.preparing++
	executorStreamLifecycle.wg.Add(1)
	return true
}

func finishExecutorStreamPreparation() {
	executorStreamLifecycle.mu.Lock()
	executorStreamLifecycle.preparing--
	executorStreamLifecycle.mu.Unlock()
	executorStreamLifecycle.wg.Done()
}

func admitExecutorStream(stream *executorStream) bool {
	executorStreamLifecycle.mu.Lock()
	defer executorStreamLifecycle.mu.Unlock()
	if executorStreamLifecycle.stopping {
		return false
	}
	executorStreamLifecycle.preparing--
	executorStreamLifecycle.active[stream] = struct{}{}
	return true
}

func unregisterExecutorStream(stream *executorStream) {
	executorStreamLifecycle.mu.Lock()
	delete(executorStreamLifecycle.active, stream)
	executorStreamLifecycle.mu.Unlock()
	executorStreamLifecycle.wg.Done()
}

func shutdownExecutorStreams() {
	executorStreamLifecycle.mu.Lock()
	executorStreamLifecycle.stopping = true
	executorStreamLifecycle.shutdowns++
	streams := make([]*executorStream, 0, len(executorStreamLifecycle.active))
	interrupts := make([]*executorStream, 0, len(executorStreamLifecycle.active))
	for stream := range executorStreamLifecycle.active {
		if !stream.terminalSelected {
			stream.terminalSelected = true
			stream.terminalErr = errExecutorStreamShutdown
		}
		if stream.terminalErr != nil && !stream.outputFinished {
			interrupts = append(interrupts, stream)
		}
		streams = append(streams, stream)
	}
	executorStreamLifecycle.mu.Unlock()
	for _, stream := range interrupts {
		_ = stream.closePlugin(stream.terminalErr.Error())
	}
	for _, stream := range streams {
		_ = stream.closeHost()
	}
	executorStreamLifecycle.wg.Wait()
	executorStreamLifecycle.mu.Lock()
	executorStreamLifecycle.shutdowns--
	executorStreamLifecycle.mu.Unlock()
}

func (s *executorStream) closeHost() error {
	s.closeHostOnce.Do(func() {
		_, s.closeHostErr = s.call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: s.hostStreamID})
	})
	return s.closeHostErr
}

func (s *executorStream) closePlugin(errText string) error {
	s.closePluginOnce.Do(func() {
		_, s.closePluginErr = s.call(pluginabi.MethodHostStreamClose, struct {
			StreamID string `json:"stream_id"`
			Error    string `json:"error,omitempty"`
		}{StreamID: s.pluginStreamID, Error: errText})
	})
	return s.closePluginErr
}

func (s *executorStream) emit(payload []byte) error {
	_, err := s.call(pluginabi.MethodHostStreamEmit, struct {
		StreamID string `json:"stream_id"`
		Payload  []byte `json:"payload"`
	}{StreamID: s.pluginStreamID, Payload: payload})
	return err
}

func prepareExecutorStream(req *executorRPCRequest, call hostCaller) (*executorStream, http.Header, error) {
	scope := callerScopeFromMetadata(req.Metadata)
	cfg := loadedConfig()
	decision, err := routeModel(cfg, req.SourceFormat, req.Model, scope, callerAPIKeyForSelectedRules(cfg, req.SourceFormat, req.Headers, req.Query, scope))
	if err != nil {
		return nil, nil, fmt.Errorf("route stream: %w", err)
	}
	if !decision.Handled {
		return nil, nil, fmt.Errorf("route stream: unhandled model route for %q", req.Model)
	}
	body, changed, err := rewriteRequestModel(req.OriginalRequest, decision.UpstreamModel)
	if err != nil {
		return nil, nil, fmt.Errorf("rewrite stream request: %w", err)
	}
	if changed {
		removeChangedBodyHeaders(req.Headers, false)
	}
	hostRaw, err := call(pluginabi.MethodHostModelExecuteStream, hostModelExecutePayload{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: req.SourceFormat,
			ExitProtocol:  req.Format,
			Model:         decision.UpstreamModel,
			Stream:        true,
			Body:          body,
			Headers:       req.Headers,
			Query:         req.Query,
			Alt:           req.Alt,
		},
		HostCallbackID: req.HostCallbackID,
	})
	releaseExecutorStreamSetup(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute stream: %w", err)
	}
	var partial struct {
		StreamID string `json:"stream_id"`
	}
	_ = json.Unmarshal(hostRaw, &partial)
	var hostResp struct {
		pluginapi.HostModelStreamResponse
		Body []byte `json:"body"`
	}
	if err := json.Unmarshal(hostRaw, &hostResp); err != nil {
		decodeErr := fmt.Errorf("decode host stream response: %w", err)
		if partial.StreamID == "" {
			return nil, nil, decodeErr
		}
		_, closeErr := call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: partial.StreamID})
		if closeErr != nil {
			return nil, nil, errors.Join(decodeErr, fmt.Errorf("close host stream: %w", closeErr))
		}
		return nil, nil, decodeErr
	}
	stream := &executorStream{
		pluginStreamID: req.StreamID,
		hostStreamID:   hostResp.StreamID,
		originalModel:  decision.OriginalModel,
		format:         req.Format,
		call:           call,
	}
	if hostResp.StatusCode >= http.StatusBadRequest {
		message := fmt.Sprintf("execute stream status %d: %s", hostResp.StatusCode, string(hostResp.Body))
		var cause error
		if stream.hostStreamID != "" {
			if closeErr := stream.closeHost(); closeErr != nil {
				cause = closeErr
				message = errors.Join(errors.New(message), fmt.Errorf("close host stream: %w", closeErr)).Error()
			}
		}
		return nil, nil, structuredError("plugin_error", message, false, hostResp.StatusCode, cause)
	}
	if stream.hostStreamID == "" {
		return nil, nil, fmt.Errorf("missing host stream id")
	}
	headers := hostResp.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	canonicalizeHeaders(headers)
	removeChangedBodyHeaders(headers, true)
	headers.Del("Transfer-Encoding")
	if headers.Get("Content-Type") == "" {
		contentType := "text/event-stream"
		if req.Format == "gemini" && req.Alt != "" {
			contentType = "application/json"
		}
		headers.Set("Content-Type", contentType)
	}
	stream.frameRawJSONAsSSE = req.Format != "gemini" && req.Format != "openai" && isEventStreamContentType(headers.Get("Content-Type"))
	return stream, headers, nil
}

func startExecutorStream(req executorRPCRequest, call hostCaller, closeStream func(string, string) error) ([]byte, error) {
	if !beginExecutorStreamPreparation() {
		return nil, fmt.Errorf("executor stream lifecycle is stopping")
	}
	stream, headers, err := prepareExecutorStream(&req, call)
	if err != nil {
		finishExecutorStreamPreparation()
		return nil, err
	}
	setup := pluginapi.ExecutorStreamResponse{Headers: headers}
	response, err := json.Marshal(struct {
		Headers http.Header `json:"headers"`
	}{Headers: setup.Headers})
	if err != nil {
		_ = stream.closeHost()
		finishExecutorStreamPreparation()
		return nil, err
	}
	if !admitExecutorStream(stream) {
		lifecycleErr := fmt.Errorf("executor stream lifecycle is stopping")
		if closeErr := stream.closeHost(); closeErr != nil {
			lifecycleErr = errors.Join(lifecycleErr, fmt.Errorf("close host stream: %w", closeErr))
		}
		finishExecutorStreamPreparation()
		return nil, lifecycleErr
	}
	go func() {
		defer unregisterExecutorStream(stream)
		if err := runStreamForward(stream); err != nil {
			outerAttempted := false
			stream.closePluginOnce.Do(func() {
				outerAttempted = true
				stream.closePluginErr = closeStream(stream.pluginStreamID, err.Error())
			})
			if !outerAttempted && stream.closePluginErr != nil {
				if !errors.Is(err, stream.closePluginErr) {
					err = joinStreamErrors(err, fmt.Errorf("close plugin stream: %w", stream.closePluginErr))
				}
				_ = closeStream(stream.pluginStreamID, err.Error())
			}
		}
	}()
	return response, nil
}

func joinStreamErrors(primary error, cleanup ...error) error {
	errs := make([]error, 0, len(cleanup)+1)
	if primary != nil {
		errs = append(errs, primary)
	}
	for _, err := range cleanup {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *executorStream) processPayload(rewriter *streamChunkRewriter, payload []byte) error {
	if len(payload) == 0 || rewriter.emitFailed {
		return nil
	}
	chunks, rewriteErr := rewriter.Write(payload)
	if rewriteErr != nil {
		rewriteErr = fmt.Errorf("rewrite stream chunk: %w", rewriteErr)
		s.selectTerminal(rewriteErr, false)
	}
	emitErr := emitRewritten(chunks, rewriter.batchSSEOutput, s.emit)
	if emitErr != nil {
		rewriter.emitFailed = true
		emitErr = fmt.Errorf("emit stream chunk: %w", emitErr)
	}
	return joinStreamErrors(rewriteErr, emitErr)
}

func (s *executorStream) flushAndEmit(rewriter *streamChunkRewriter, cleanCompletion bool) error {
	var (
		flushed [][]byte
		err     error
	)
	if cleanCompletion {
		flushed, err = rewriter.Finish()
	} else {
		flushed, err = rewriter.Flush()
	}
	if err != nil {
		err = fmt.Errorf("flush stream rewriter: %w", err)
		s.selectTerminal(err, false)
	}
	if rewriter.emitFailed {
		return err
	}
	emitErr := emitRewritten(flushed, rewriter.batchSSEOutput, s.emit)
	if emitErr != nil {
		rewriter.emitFailed = true
		emitErr = fmt.Errorf("emit flushed stream chunk: %w", emitErr)
	}
	return joinStreamErrors(err, emitErr)
}

func (s *executorStream) selectTerminal(primary error, completed bool) error {
	executorStreamLifecycle.mu.Lock()
	if completed {
		// 输出和 flush 结束后，由 worker 合并 host cleanup 错误再关闭下游。
		s.outputFinished = true
	}
	// 正常输出完成后才能选定自然结束；选定后的 terminalErr 保持不变。
	if !s.terminalSelected && (primary != nil || completed) {
		s.terminalSelected = true
		s.terminalErr = primary
	}
	terminalErr := s.terminalErr
	executorStreamLifecycle.mu.Unlock()
	if terminalErr != nil && !errors.Is(primary, terminalErr) {
		return joinStreamErrors(primary, terminalErr)
	}
	return primary
}

func (s *executorStream) finish(rewriter *streamChunkRewriter, primary error, payloadErr error, closePlugin bool, cleanCompletion bool) error {
	primary = s.selectTerminal(joinStreamErrors(primary, payloadErr), false)
	flushErr := s.flushAndEmit(rewriter, cleanCompletion && primary == nil)
	firstErr := s.selectTerminal(joinStreamErrors(primary, flushErr), true)
	if err := s.closeHost(); err != nil {
		firstErr = joinStreamErrors(firstErr, fmt.Errorf("close host stream: %w", err))
	}
	if !closePlugin {
		return firstErr
	}
	errText := ""
	if firstErr != nil {
		errText = firstErr.Error()
	}
	if err := s.closePlugin(errText); err != nil {
		return joinStreamErrors(firstErr, fmt.Errorf("close plugin stream: %w", err))
	}
	return nil
}

func runStreamForward(stream *executorStream) error {
	rewriter := newStreamChunkRewriter(stream.originalModel)
	rewriter.format = stream.format
	rewriter.frameRawJSONAsSSE = stream.frameRawJSONAsSSE
	for {
		executorStreamLifecycle.mu.Lock()
		terminalErr := stream.terminalErr
		executorStreamLifecycle.mu.Unlock()
		if terminalErr != nil {
			return stream.finish(rewriter, terminalErr, nil, true, false)
		}
		readRaw, err := stream.call(pluginabi.MethodHostModelStreamRead, pluginapi.HostModelStreamReadRequest{StreamID: stream.hostStreamID})
		if err != nil {
			return stream.finish(rewriter, fmt.Errorf("read host stream: %w", err), nil, false, false)
		}
		var chunk pluginapi.HostModelStreamReadResponse
		if err := json.Unmarshal(readRaw, &chunk); err != nil {
			return stream.finish(rewriter, fmt.Errorf("decode host stream chunk: %w", err), nil, false, false)
		}
		var primary error
		if chunk.Error != "" {
			primary = stream.selectTerminal(errors.New(chunk.Error), false)
		}
		payloadErr := stream.processPayload(rewriter, chunk.Payload)
		if primary != nil {
			return stream.finish(rewriter, primary, payloadErr, true, false)
		}
		if payloadErr != nil || chunk.Done {
			return stream.finish(rewriter, payloadErr, nil, true, chunk.Done && payloadErr == nil)
		}
	}
}
