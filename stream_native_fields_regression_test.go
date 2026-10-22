package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const gNativeOutput = `[{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}]`
const gNativeCompleted = `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.7","output":` + gNativeOutput + `}}`
const gNativeCompletedWant = `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.6","output":` + gNativeOutput + `}}`

func gNativeParts(t *testing.T, format string, finish bool, parts ...[]byte) []byte {
	t.Helper()
	r := newStreamChunkRewriter("grok-4.6")
	r.format, r.frameRawJSONAsSSE = format, true
	var out []byte
	for _, part := range parts {
		chunks, err := r.Write(part)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, bytes.Join(chunks, nil)...)
	}
	var chunks [][]byte
	var err error
	if finish {
		chunks, err = r.Finish()
	} else {
		chunks, err = r.Flush()
	}
	if err != nil {
		t.Fatal(err)
	}
	return append(out, bytes.Join(chunks, nil)...)
}

func TestFunctionalNativeSSEFieldError(t *testing.T) {
	const data = `{"type":"error","code":"server_error","message":"failed","param":null,"opaque":{"n":1.00,"text":"grok-4.7 中文"}}`
	for _, eol := range []string{"\n", "\r", "\r\n"} {
		t.Run(fmt.Sprintf("line-header/eol=%q", eol), func(t *testing.T) {
			got := gNativeParts(t, "openai-response", true, []byte("event: error"+eol), []byte("data: "+data))
			want := []byte("event: error" + eol + "data: " + data + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("line error=%q want=%q", got, want)
			}
		})
	}
	for _, finish := range []bool{false, true} {
		t.Run(fmt.Sprintf("data-only/finish=%v", finish), func(t *testing.T) {
			got := gNativeParts(t, "openai-response", finish, []byte("data: "+data))
			want := []byte("data: " + data + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("data-only error=%q want=%q", got, want)
			}
			requireValidResponsesSSE(t, got, 1)
		})
		t.Run(fmt.Sprintf("pair/finish=%v", finish), func(t *testing.T) {
			got := gNativeParts(t, "openai-response", finish, []byte("event: error"), []byte("data: "+data))
			want := []byte("event: error\ndata: " + data + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("error fields=%q want=%q", got, want)
			}
			requireValidResponsesSSE(t, got, 1)
		})
	}
}

func TestFunctionalNativeSSEFieldKeepalive(t *testing.T) {
	const data = `{"type":"keepalive","opaque":{"n":1.00,"text":"grok-4.7 中文"}}`
	for _, finish := range []bool{false, true} {
		t.Run(fmt.Sprintf("continuous-data-only/finish=%v", finish), func(t *testing.T) {
			got := gNativeParts(t, "openai-response", finish, []byte("data: "+data), []byte("data: "+gNativeCompleted))
			want := []byte("data: " + data + "\n\ndata: " + gNativeCompletedWant + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("continuous keepalive=%q want=%q", got, want)
			}
			requireValidResponsesSSE(t, got, 2)
		})
	}
	for _, eol := range []string{"", "\n", "\r", "\r\n"} {
		t.Run(fmt.Sprintf("pair/eol=%q", eol), func(t *testing.T) {
			parts := [][]byte{[]byte("event: keepalive" + eol), []byte("data: " + data), []byte("event: response.completed"), []byte("data: " + gNativeCompleted)}
			lineEnd := eol
			if lineEnd == "" {
				lineEnd = "\n"
			}
			want := []byte("event: keepalive" + lineEnd + "data: " + data + "\n\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
			got := gNativeParts(t, "openai-response", true, parts...)
			if !bytes.Equal(got, want) {
				t.Fatalf("keepalive pair=%q want=%q", got, want)
			}
			normalized := bytes.ReplaceAll(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
			requireValidResponsesSSE(t, normalized, 2)
		})
	}
}

func TestFunctionalNativeSSEFieldDoneAlias(t *testing.T) {
	t.Run("whitelist-and-opaque", func(t *testing.T) {
		data := `{"type":"response.completed","event_type":17,"model":"grok-4.7","modelVersion":"grok-4.7","response":{"model":"grok-4.7","modelVersion":"grok-4.7","output":` + gNativeOutput + `},"message":{"model":"grok-4.7"},"interaction":{"model":"grok-4.7"},"opaque":{"model":"grok-4.7","n":1.00,"text":"event: response.done data: grok-4.7 中文"}}`
		wantData := `{"type":"response.completed","event_type":17,"model":"grok-4.6","modelVersion":"grok-4.6","response":{"model":"grok-4.6","modelVersion":"grok-4.6","output":` + gNativeOutput + `},"message":{"model":"grok-4.6"},"interaction":{"model":"grok-4.6"},"opaque":{"model":"grok-4.7","n":1.00,"text":"event: response.done data: grok-4.7 中文"}}`
		got := gNativeParts(t, "openai-response", true, []byte("event: response.done"), []byte("data: "+data))
		if want := []byte("event: response.done\ndata: " + wantData + "\n\n"); !bytes.Equal(got, want) {
			t.Fatalf("whitelist alias=%q want=%q", got, want)
		}
	})
	t.Run("delimiterless-mismatch-controls", func(t *testing.T) {
		for _, tc := range []struct{ header, data string }{
			{"response.completed", `{"type":"response.done","model":"grok-4.7"}`},
			{"response.done", `{"type":"unknown","model":"grok-4.7"}`},
			{"response.done", `{"model":"grok-4.7"}`},
			{"response.done", `{"type":null,"model":"grok-4.7"}`},
			{"response.done", `{"type":17,"model":"grok-4.7"}`},
			{"response.done", `{"type":false,"model":"grok-4.7"}`},
			{"error", gNativeCompleted},
			{"keepalive", `{"type":"error","code":"server_error"}`},
			{"error", `{"type":"keepalive"}`},
			{"response.opaque text", `{"type":"error","model":"grok-4.7"}`},
			{"response.opaque text", `{"type":"keepalive","model":"grok-4.7"}`},
		} {
			parts := [][]byte{[]byte("event: " + tc.header), []byte("data: " + tc.data)}
			if got := gNativeParts(t, "openai-response", true, parts...); !bytes.Equal(got, bytes.Join(parts, nil)) {
				t.Fatalf("mismatch header=%q data=%q changed to=%q", tc.header, tc.data, got)
			}
		}
	})
	t.Run("logical-metadata-callback-characterization", func(t *testing.T) {
		parts := [][]byte{[]byte("\xef\xbb\xbfid:"), []byte(" event-1\nevent: error\ndata: " + gNativeCompleted + "\n\n")}
		want := []byte("id:\n event-1\nevent: error\ndata: " + gNativeCompletedWant + "\n\n")
		if got := gNativeParts(t, "openai-response", true, parts...); !bytes.Equal(got, want) {
			t.Fatalf("partial metadata callback=%q want existing bytes=%q", got, want)
		}
	})
	t.Run("generic-standard-wire", func(t *testing.T) {
		for _, header := range []string{"error", "keepalive", "response.done", "response.completed"} {
			for _, eol := range []string{"\n", "\r", "\r\n"} {
				for _, format := range []string{"openai-response", "claude"} {
					wire := []byte("\xef\xbb\xbfid: event-1" + eol + "event: " + header + eol + ": opaque grok-4.7 中文" + eol + "data: " + gNativeCompleted + eol + "retry: 100" + eol + eol)
					want := []byte("id: event-1" + eol + "event: " + header + eol + ": opaque grok-4.7 中文" + eol + "data: " + gNativeCompletedWant + eol + "retry: 100" + eol + eol)
					if format == "openai-response" {
						if got := gNativeParts(t, format, true, wire); !bytes.Equal(got, want) {
							t.Fatalf("standard Responses header=%s eol=%q output=%q want=%q", header, eol, got, want)
						}
						continue
					}
					for split := 0; split <= len(wire); split++ {
						if got := gNativeParts(t, format, true, wire[:split], wire[split:]); !bytes.Equal(got, want) {
							t.Fatalf("header=%s eol=%q format=%s split=%d wire=%q want=%q", header, eol, format, split, got, want)
						}
					}
					parts := make([][]byte, len(wire))
					for i := range wire {
						parts[i] = wire[i : i+1]
					}
					if got := gNativeParts(t, format, true, parts...); !bytes.Equal(got, want) {
						t.Fatalf("bytewise header=%s eol=%q format=%s output=%q want=%q", header, eol, format, got, want)
					}
				}
			}
		}
	})
	for _, created := range []bool{false, true} {
		for _, finish := range []bool{false, true} {
			t.Run(fmt.Sprintf("created=%v/finish=%v", created, finish), func(t *testing.T) {
				parts := [][]byte{[]byte("event: response.done"), []byte("data: " + gNativeCompleted)}
				want := []byte("event: response.done\ndata: " + gNativeCompletedWant + "\n\n")
				count := 1
				if created {
					prefix, restored, _ := gNativeFields(false, 2)
					parts = append(prefix[:2], parts...)
					want = append(bytes.SplitAfter(restored, []byte("\n\n"))[0], want...)
					count++
				}
				got := gNativeParts(t, "openai-response", finish, parts...)
				if !bytes.Equal(got, want) {
					t.Fatalf("done alias=%q want=%q", got, want)
				}
				requireValidResponsesSSE(t, got, count)
			})
		}
	}
}

func TestFunctionalNativeSSEFieldTerminalDispatch(t *testing.T) {
	t.Run("full-comment-with-pending-metadata", func(t *testing.T) {
		r := newStreamChunkRewriter("grok-4.6")
		r.format, r.frameRawJSONAsSSE = "openai-response", true
		parts := []string{`data: {"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`, "id: terminal-1", "retry: 100", ": opaque 中文 grok-4.7", ": keepalive\n\n", "data: " + gNativeCompleted}
		wantPrefix := []byte("data: {\"type\":\"response.created\",\"response\":{\"model\":\"grok-4.6\",\"status\":\"in_progress\",\"output\":[]}}\n\nid: terminal-1\nretry: 100\n: opaque 中文 grok-4.7\n: keepalive\n\n")
		var got []byte
		for i, part := range parts {
			chunks, err := r.Write([]byte(part))
			want := []byte(nil)
			if i == 4 {
				want = wantPrefix
			}
			if err != nil || !bytes.Equal(bytes.Join(chunks, nil), want) {
				t.Fatalf("Write[%d]=%q error=%v want=%q", i, chunks, err, want)
			}
			got = append(got, bytes.Join(chunks, nil)...)
		}
		chunks, err := r.Finish()
		if err != nil || !bytes.Equal(bytes.Join(chunks, nil), []byte("data: "+gNativeCompletedWant+"\n\n")) {
			t.Fatalf("Finish=%q error=%v", chunks, err)
		}
		got = append(got, bytes.Join(chunks, nil)...)
		if want := append(bytes.Clone(wantPrefix), []byte("data: "+gNativeCompletedWant+"\n\n")...); !bytes.Equal(got, want) {
			t.Fatalf("complete output=%q want=%q", got, want)
		}
		requireValidResponsesSSE(t, bytes.Replace(got, []byte("id: terminal-1\nretry: 100\n: opaque 中文 grok-4.7\n: keepalive\n\n"), nil, 1), 2)
	})
	for _, finish := range []bool{false, true} {
		t.Run(fmt.Sprintf("data-only-created/full-comment/finish=%v", finish), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			parts := []string{
				`data: {"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
				": keepalive\n\n",
				"data: " + gNativeCompleted,
			}
			wantCreated := []byte("data: {\"type\":\"response.created\",\"response\":{\"model\":\"grok-4.6\",\"status\":\"in_progress\",\"output\":[]}}\n\n: keepalive\n\n")
			var got []byte
			for i, part := range parts {
				chunks, err := r.Write([]byte(part))
				want := []byte(nil)
				if i == 1 {
					want = wantCreated
				}
				if err != nil || !bytes.Equal(bytes.Join(chunks, nil), want) {
					t.Fatalf("Write[%d]=%q error=%v want=%q", i, chunks, err, want)
				}
				got = append(got, bytes.Join(chunks, nil)...)
			}
			for i := 0; i < 2; i++ {
				var chunks [][]byte
				var err error
				if finish {
					chunks, err = r.Finish()
				} else {
					chunks, err = r.Flush()
				}
				want := []byte(nil)
				if i == 0 {
					want = []byte("data: " + gNativeCompletedWant + "\n\n")
				}
				if err != nil || !bytes.Equal(bytes.Join(chunks, nil), want) {
					t.Fatalf("finish=%v call=%d output=%q error=%v want=%q", finish, i, chunks, err, want)
				}
				got = append(got, bytes.Join(chunks, nil)...)
			}
			want := append(bytes.Clone(wantCreated), []byte("data: "+gNativeCompletedWant+"\n\n")...)
			if !bytes.Equal(got, want) || bytes.Count(got, []byte(": keepalive\n\n")) != 1 {
				t.Fatalf("complete output=%q want=%q", got, want)
			}
			requireValidResponsesSSE(t, bytes.Replace(got, []byte(": keepalive\n\n"), nil, 1), 2)
		})
	}
	for _, terminal := range []string{"error", "keepalive", "done-alias"} {
		for _, shape := range []string{"pair", "data-only", "alternating-pair", "alternating-data"} {
			for _, position := range []string{"none", "before-event", "in-event", "between-events"} {
				t.Run(terminal+"/"+shape+"/"+position, func(t *testing.T) {
					names := []string{"response.created", "error"}
					payloads := []string{`{"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`, `{"type":"error","code":"server_error","message":"failed","param":null,"opaque":{"n":1.00}}`}
					if terminal == "keepalive" {
						names = []string{"response.created", "keepalive", "response.completed"}
						payloads = []string{payloads[0], `{"type":"keepalive","event_type":false,"opaque":{"n":1.00}}`, gNativeCompleted}
					} else if terminal == "done-alias" {
						names[1], payloads[1] = "response.done", gNativeCompleted
					}
					metadata := []string{"id: terminal-1", "retry: 100", `: opaque event: error data: {"type":"keepalive"} grok-4.7 中文`}
					r := newStreamChunkRewriter("grok-4.6")
					r.format, r.frameRawJSONAsSSE = "openai-response", true
					var got, want []byte
					var units [][]byte
					pendingData := false
					write := func(part string) {
						t.Helper()
						chunks, err := r.Write([]byte(part))
						got = append(got, bytes.Join(chunks, nil)...)
						if err != nil || !bytes.Equal(got, want) {
							t.Fatalf("write=%q output=%q want=%q error=%v", part, got, want, err)
						}
						if len(want) > 0 {
							requireValidResponsesSSE(t, got, len(bytes.SplitAfter(want, []byte("\n\n")))-1)
						}
					}
					for i, name := range names {
						dataOnly := shape == "data-only" || shape == "alternating-data" && i%2 == 0 || shape == "alternating-pair" && i%2 == 1
						var unit []byte
						if position == "before-event" || position == "between-events" && i > 0 || position == "in-event" && dataOnly {
							for _, field := range metadata {
								write(field)
								unit = append(unit, []byte(field+"\n")...)
							}
						}
						if pendingData {
							want = append(want, units[i-1]...)
							pendingData = false
						}
						if !dataOnly {
							write("event: " + name)
							unit = append(unit, []byte("event: "+name+"\n")...)
							if position == "in-event" {
								for _, field := range metadata {
									write(field)
									unit = append(unit, []byte(field+"\n")...)
								}
							}
						}
						restored := strings.ReplaceAll(payloads[i], `"model":"grok-4.7"`, `"model":"grok-4.6"`)
						unit = append(unit, []byte("data: "+restored+"\n\n")...)
						units = append(units, unit)
						if !dataOnly {
							want = append(want, unit...)
						} else {
							pendingData = true
						}
						write("data: " + payloads[i])
					}
					if pendingData {
						want = append(want, units[len(units)-1]...)
					}
					chunks, err := r.Finish()
					got = append(got, bytes.Join(chunks, nil)...)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("finish=%q want=%q error=%v", got, want, err)
					}
					requireValidResponsesSSE(t, got, len(names))
					for _, delimiter := range []string{"\n\n", "\r\n\r\n"} {
						chunks, err := r.Write([]byte(delimiter))
						if err != nil || len(bytes.TrimSpace(bytes.Join(chunks, nil))) != 0 {
							t.Fatalf("late delimiter=%q output=%q error=%v", delimiter, chunks, err)
						}
					}
				})
			}
		}
	}
}

func TestFunctionalNativeSSEFieldTerminalForwarder(t *testing.T) {
	for _, event := range []string{"error", "keepalive", "response.done", "grok-comment"} {
		for _, dataOnly := range []bool{false, true} {
			terminals := []string{"natural", "done-payload", "in-band-error", "callback-error", "cleanup-errors", "emit-error"}
			if event == "grok-comment" {
				terminals = append(terminals, "prefix-error")
			}
			for _, terminal := range terminals {
				t.Run(fmt.Sprintf("%s/dataOnly=%v/%s", event, dataOnly, terminal), func(t *testing.T) {
					data := `{"type":"error","code":"server_error","message":"failed","param":null}`
					if event == "keepalive" {
						data = `{"type":"keepalive"}`
					} else if event == "response.done" {
						data = gNativeCompleted
					}
					parts := [][]byte{[]byte("data: " + data)}
					want := "data: " + strings.ReplaceAll(data, `"model":"grok-4.7"`, `"model":"grok-4.6"`) + "\n\n"
					if !dataOnly {
						parts = append([][]byte{[]byte("event: " + event)}, parts...)
						want = "event: " + event + "\n" + want
					}
					if event == "keepalive" {
						if !dataOnly {
							parts = append(parts, []byte("event: response.completed"))
							want += "event: response.completed\n"
						}
						parts = append(parts, []byte("data: "+gNativeCompleted))
						want += "data: " + gNativeCompletedWant + "\n\n"
					}
					if event == "grok-comment" {
						parts = [][]byte{[]byte(`data: {"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`), []byte(": keepalive\n\n"), []byte("data: " + gNativeCompleted)}
						want = "data: {\"type\":\"response.created\",\"response\":{\"model\":\"grok-4.6\",\"status\":\"in_progress\",\"output\":[]}}\n\n: keepalive\n\ndata: " + gNativeCompletedWant + "\n\n"
						if !dataOnly {
							parts = append([][]byte{[]byte("event: response.created")}, parts...)
							want = "event: response.created\n" + want
						}
						if terminal == "prefix-error" {
							parts[len(parts)-1] = append(parts[len(parts)-1], []byte("event: response.completed\ndata: {\"output\":\""+strings.Repeat("x", maxPendingStreamBytes+1))...)
						}
					}
					reads := make([]pluginapi.HostModelStreamReadResponse, len(parts)+1)
					for i, part := range parts {
						reads[i].Payload = part
					}
					reads[len(parts)].Done = true
					if terminal == "done-payload" {
						reads[len(parts)-1].Done = true
					} else if terminal == "in-band-error" || terminal == "cleanup-errors" {
						reads[len(parts)-1].Done, reads[len(parts)-1].Error = true, "original upstream terminal error"
					}
					readErr, hostErr, pluginErr, emitErr := errors.New("original callback error"), errors.New("original host close error"), errors.New("original plugin close error"), errors.New("original emit error")
					var got []byte
					var order []string
					closeText := ""
					hostCloses, pluginCloses, readCount := 0, 0, 0
					stream := &executorStream{pluginStreamID: "terminal", hostStreamID: "terminal-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
					stream.call = func(method string, payload any) (json.RawMessage, error) {
						switch method {
						case pluginabi.MethodHostModelStreamRead:
							readCount++
							if terminal == "callback-error" && readCount == len(parts)+1 {
								return nil, readErr
							}
							if len(reads) == 0 {
								return nil, errors.New("unexpected extra terminal read")
							}
							read := reads[0]
							reads = reads[1:]
							return json.Marshal(read)
						case pluginabi.MethodHostStreamEmit:
							order = append(order, "emit")
							raw, err := json.Marshal(payload)
							if err != nil {
								return nil, err
							}
							var emit struct{ Payload []byte }
							if err := json.Unmarshal(raw, &emit); err != nil {
								return nil, err
							}
							got = append(got, emit.Payload...)
							if terminal == "emit-error" {
								return nil, emitErr
							}
						case pluginabi.MethodHostModelStreamClose:
							hostCloses++
							order = append(order, "host-close")
							if terminal == "cleanup-errors" {
								return nil, hostErr
							}
						case pluginabi.MethodHostStreamClose:
							pluginCloses++
							order = append(order, "plugin-close")
							raw, err := json.Marshal(payload)
							if err != nil {
								return nil, err
							}
							var closed struct{ Error string }
							if err := json.Unmarshal(raw, &closed); err != nil {
								return nil, err
							}
							closeText = closed.Error
							if terminal == "cleanup-errors" {
								return nil, pluginErr
							}
						default:
							return nil, fmt.Errorf("unexpected terminal callback %s", method)
						}
						return json.RawMessage(`{}`), nil
					}
					err := runStreamForward(stream)
					if terminal == "emit-error" && event == "keepalive" {
						want = strings.SplitAfter(want, "\n\n")[0]
					}
					if terminal == "emit-error" && event == "grok-comment" {
						want = strings.SplitAfter(want, "\n\n")[0]
						if dataOnly {
							want += ": keepalive\n\n"
						}
					}
					if string(got) != want {
						t.Fatalf("forwarded=%q want=%q", got, want)
					}
					count := 1
					if (event == "keepalive" || event == "grok-comment") && terminal != "emit-error" {
						count++
					}
					requireValidResponsesSSE(t, bytes.Replace(got, []byte(": keepalive\n\n"), nil, 1), count)
					wantOrder := []string{"emit", "host-close", "plugin-close"}
					if terminal == "callback-error" {
						wantOrder = wantOrder[:2]
					}
					if len(order) < len(wantOrder) || strings.Join(order[len(order)-len(wantOrder):], ",") != strings.Join(wantOrder, ",") || hostCloses != 1 {
						t.Fatalf("close order=%v host closes=%d want suffix=%v", order, hostCloses, wantOrder)
					}
					if event == "grok-comment" {
						wantReads, wantEmits := len(parts), 2
						if !dataOnly {
							wantEmits = 3
						}
						if terminal == "natural" || terminal == "callback-error" {
							wantReads++
						} else if terminal == "emit-error" {
							wantReads, wantEmits = 2, 1
						}
						if readCount != wantReads || len(order)-hostCloses-pluginCloses != wantEmits {
							t.Fatalf("reads=%d want=%d order=%v want emits=%d", readCount, wantReads, order, wantEmits)
						}
					}
					switch terminal {
					case "callback-error":
						if !errors.Is(err, readErr) || pluginCloses != 0 || readCount != len(parts)+1 {
							t.Fatalf("callback error=%v plugin closes=%d reads=%d", err, pluginCloses, readCount)
						}
					case "cleanup-errors":
						if !errors.Is(err, hostErr) || !errors.Is(err, pluginErr) || !strings.Contains(closeText, "original upstream terminal error") || !strings.Contains(closeText, hostErr.Error()) || pluginCloses != 1 {
							t.Fatalf("cleanup error=%v close=%q plugin closes=%d", err, closeText, pluginCloses)
						}
					default:
						wantError := ""
						if terminal == "in-band-error" {
							wantError = "original upstream terminal error"
						} else if terminal == "emit-error" {
							wantError = "emit stream chunk: " + emitErr.Error()
							if dataOnly && event != "keepalive" && event != "grok-comment" {
								wantError = "emit flushed stream chunk: " + emitErr.Error()
							}
						} else if terminal == "prefix-error" {
							wantError = fmt.Sprintf("rewrite stream chunk: stream pending data exceeds %d bytes", maxPendingStreamBytes)
						}
						if err != nil || pluginCloses != 1 || closeText != wantError {
							t.Fatalf("terminal error=%v plugin closes=%d close=%q want=%q", err, pluginCloses, closeText, wantError)
						}
					}
				})
			}
		}
	}
}

func TestFunctionalNativeSSEFieldBoundary(t *testing.T) {
	for _, finish := range []bool{false, true} {
		t.Run(fmt.Sprintf("finish=%v", finish), func(t *testing.T) {
			got := gNativeParts(t, "openai-response", finish,
				[]byte("event: response.completed"), []byte("data: "+gNativeCompleted))
			want := []byte("event: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("native fields output=%q, want %q", got, want)
			}
			requireValidResponsesSSE(t, got, 1)
			var event struct {
				Response struct {
					Model  string
					Output json.RawMessage
				}
			}
			_, _, rest := splitSSELine(got)
			data, _, _ := splitSSELine(rest)
			if err := json.Unmarshal(sseFieldValue(data), &event); err != nil {
				t.Fatal(err)
			}
			if event.Response.Model != "grok-4.6" || string(event.Response.Output) != gNativeOutput {
				t.Fatalf("response=%+v", event.Response)
			}
		})
	}
}

func gNativeSequence() ([]string, []string) {
	names := []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed"}
	payloads := []string{
		`{"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
		`{"type":"response.in_progress","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ordinary grok-4.7 opaque 中文 output"}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"ordinary grok-4.7 opaque 中文 output"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}}`,
		gNativeCompleted,
	}
	return names, payloads
}

func TestFunctionalNativeSSEFieldForwarder(t *testing.T) {
	for _, terminal := range []string{"natural", "in-band-error", "callback-error"} {
		t.Run(terminal, func(t *testing.T) {
			reads := []pluginapi.HostModelStreamReadResponse{
				{Payload: []byte("event: response.completed")},
				{Payload: []byte("data: " + gNativeCompleted)},
				{Done: true},
			}
			if terminal == "in-band-error" {
				reads[1].Error, reads[1].Done = "controlled upstream read error", true
			}
			var emitted []byte
			var closeText string
			hostCloses, pluginCloses := 0, 0
			readErr := errors.New("controlled callback read error")
			stream := &executorStream{pluginStreamID: "g-native", hostStreamID: "g-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
			stream.call = func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostModelStreamRead:
					if terminal == "callback-error" && len(reads) == 1 {
						return nil, readErr
					}
					if len(reads) == 0 {
						return nil, errors.New("unexpected extra host read")
					}
					read := reads[0]
					reads = reads[1:]
					return json.Marshal(read)
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
				case pluginabi.MethodHostModelStreamClose:
					hostCloses++
				case pluginabi.MethodHostStreamClose:
					pluginCloses++
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var closed struct {
						Error string `json:"error"`
					}
					if err := json.Unmarshal(raw, &closed); err != nil {
						return nil, err
					}
					closeText = closed.Error
				default:
					return nil, fmt.Errorf("unexpected callback %s", method)
				}
				return json.RawMessage(`{}`), nil
			}
			err := runStreamForward(stream)
			want := []byte("event: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
			if !bytes.Equal(emitted, want) {
				t.Fatalf("forwarded native fields=%q, want %q", emitted, want)
			}
			if hostCloses != 1 {
				t.Fatalf("host closes=%d", hostCloses)
			}
			if terminal == "callback-error" {
				if !errors.Is(err, readErr) || pluginCloses != 0 {
					t.Fatalf("error=%v plugin closes=%d", err, pluginCloses)
				}
			} else {
				if err != nil || pluginCloses != 1 {
					t.Fatalf("error=%v plugin closes=%d", err, pluginCloses)
				}
				if terminal == "natural" && closeText != "" {
					t.Fatal(closeText)
				}
				if terminal == "in-band-error" && closeText != "controlled upstream read error" {
					t.Fatalf("terminal error=%q", closeText)
				}
			}
		})
	}
}

func TestFunctionalNativeSSEFieldWireControls(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n", "\r"} {
		input := []byte("event: response.completed" + eol + "data: " + gNativeCompleted + eol + "id: event-1" + eol + "retry: 100" + eol + "x-vendor-field: grok-4.7" + eol + ": opaque grok-4.7" + eol + eol)
		want := []byte("event: response.completed" + eol + "data: " + gNativeCompletedWant + eol + "id: event-1" + eol + "retry: 100" + eol + "x-vendor-field: grok-4.7" + eol + ": opaque grok-4.7" + eol + eol)
		for split := 0; split <= len(input); split++ {
			got := gNativeParts(t, "claude", true, input[:split], input[split:])
			if !bytes.Equal(got, want) {
				t.Fatalf("eol=%q split=%d output=%q, want %q", eol, split, got, want)
			}
		}
		parts := make([][]byte, len(input))
		for i := range input {
			parts[i] = input[i : i+1]
		}
		if got := gNativeParts(t, "claude", true, parts...); !bytes.Equal(got, want) {
			t.Fatalf("eol=%q bytewise output differs", eol)
		}
	}
	literal := []byte("event: response.completeddata: " + gNativeCompleted + "\n\n")
	for split := 0; split <= len(literal); split++ {
		if got := gNativeParts(t, "claude", true, literal[:split], literal[split:]); !bytes.Equal(got, literal) {
			t.Fatalf("literal event value split=%d changed to %q", split, got)
		}
	}
	r := newStreamChunkRewriter("grok-4.6")
	r.format, r.frameRawJSONAsSSE = "claude", true
	if chunks, err := r.Write([]byte("event: response.completed\ndata: " + gNativeCompleted)); err != nil || len(chunks) != 0 {
		t.Fatalf("ordinary pre-metadata output=%q error=%v", chunks, err)
	}
	chunks, err := r.Write([]byte("\nid: event-1\n\n"))
	want := []byte("event: response.completed\ndata: " + gNativeCompletedWant + "\nid: event-1\n\n")
	if err != nil || !bytes.Equal(bytes.Join(chunks, nil), want) {
		t.Fatalf("metadata output=%q error=%v", chunks, err)
	}
}

func TestFunctionalNativeSSEFieldFormatIsolation(t *testing.T) {
	for _, format := range []string{"openai", "claude", "gemini", "interactions"} {
		parts := [][]byte{[]byte("event: response.completed"), []byte("data: " + gNativeCompleted)}
		got := gNativeParts(t, format, true, parts...)
		if !bytes.Equal(got, bytes.Join(parts, nil)) {
			t.Fatalf("format=%s inactive native recovery=%q", format, got)
		}
	}
}

func TestFunctionalNativeSSEFieldDiscriminator(t *testing.T) {
	for _, unused := range []string{"17", "false"} {
		t.Run("unused="+unused, func(t *testing.T) {
			data := `{"type":"response.completed","event_type":` + unused + `,"response":{"model":"grok-4.7"},"opaque":{"text":"event: response.completed data: grok-4.7","n":1.00}}`
			wantData := `{"type":"response.completed","event_type":` + unused + `,"response":{"model":"grok-4.6"},"opaque":{"text":"event: response.completed data: grok-4.7","n":1.00}}`
			got := gNativeParts(t, "openai-response", true, []byte("event: response.completed"), []byte("data: "+data))
			want := []byte("event: response.completed\ndata: " + wantData + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("unused=%s output=%q, want %q", unused, got, want)
			}
		})
	}
	t.Run("mismatch-control", func(t *testing.T) {
		parts := [][]byte{[]byte("event: response.completed"), []byte(`data: {"type":"response.created","response":{"model":"grok-4.7"}}`)}
		if got := gNativeParts(t, "openai-response", true, parts...); !bytes.Equal(got, bytes.Join(parts, nil)) {
			t.Fatalf("mismatched discriminator changed to %q", got)
		}
	})
}

func TestFunctionalNativeSSEFieldDataOnlyTypeControl(t *testing.T) {
	for _, value := range []string{`{}`, `{"type":""}`, `{"type":null}`, `{"type":17}`, `{"type":false}`, `{"type":"other"}`} {
		t.Run(value, func(t *testing.T) {
			input := []byte("data: " + value)
			if got := gNativeParts(t, "openai-response", true, input); !bytes.Equal(got, input) {
				t.Fatalf("unrecognized type output=%q want=%q", got, input)
			}
		})
	}
}

func TestFunctionalNativeSSEFieldNativeDelimiter(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n", "\r"} {
		t.Run(fmt.Sprintf("eol=%q", eol), func(t *testing.T) {
			data := []byte("data: " + gNativeCompleted + eol + eol)
			want := []byte("event: response.completed\ndata: " + gNativeCompletedWant + eol + eol)
			if got := gNativeParts(t, "openai-response", true, []byte("event: response.completed"), data); !bytes.Equal(got, want) {
				t.Fatalf("native delimiter output=%q want=%q", got, want)
			}
		})
	}
}

func TestFunctionalNativeSSEFieldLimit(t *testing.T) {
	if maxPendingStreamBytes != 16<<20 {
		t.Fatal("pending limit changed")
	}
	for _, dataOnly := range []bool{false, true} {
		header := []byte("event: response.output_text.done")
		if dataOnly {
			header = nil
		}
		value := `{"type":"response.output_text.done","text":"` + strings.Repeat("x", maxPendingStreamBytes) + `"}`
		data := []byte("data: " + value)
		unitWant := []byte("data: " + value + "\n\n")
		if !dataOnly {
			unitWant = append([]byte("event: response.output_text.done\n"), unitWant...)
		}
		cut := maxPendingStreamBytes - len(header)
		for name, parts := range map[string][][]byte{
			"single-complete":                {header, data},
			"two-complete":                   {header, data, header, data},
			"pending-at-limit-then-complete": {header, data[:cut], data[cut:], header, data},
		} {
			t.Run(fmt.Sprintf("dataOnly=%v/%s", dataOnly, name), func(t *testing.T) {
				want := bytes.Clone(unitWant)
				if name != "single-complete" {
					want = append(want, unitWant...)
				}
				got := gNativeParts(t, "openai-response", true, parts...)
				if !bytes.Equal(bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatalf("complete native bytes=%d want=%d", len(got), len(want))
				}
			})
		}
		t.Run(fmt.Sprintf("dataOnly=%v/incomplete-control", dataOnly), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			if _, err := r.Write(header); err != nil {
				t.Fatal(err)
			}
			chunks, err := r.Write([]byte(`data: {"type":"response.output_text.done","text":"` + strings.Repeat("x", maxPendingStreamBytes)))
			if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") || len(chunks) != 0 {
				t.Fatalf("incomplete native=(%d chunks,%v)", len(chunks), err)
			}
			chunks, err = r.Flush()
			if err != nil || len(bytes.Join(chunks, nil)) != 0 {
				t.Fatalf("cleared incomplete flush=(%d chunks,%v)", len(chunks), err)
			}
		})
	}
}

func gNativeFields(dataOnly bool, count int) ([][]byte, []byte, []string) {
	names, payloads := gNativeSequence()
	indices := []int{8}
	if count == 2 {
		indices = []int{0, 8}
	} else if count == 9 {
		indices = []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	}
	var parts [][]byte
	var want bytes.Buffer
	var types []string
	for _, i := range indices {
		if !dataOnly {
			parts = append(parts, []byte("event: "+names[i]))
			fmt.Fprintf(&want, "event: %s\n", names[i])
		}
		parts = append(parts, []byte("data: "+payloads[i]))
		restored := payloads[i]
		if i < 2 || i == 8 {
			restored = strings.Replace(restored, `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1)
		}
		fmt.Fprintf(&want, "data: %s\n\n", restored)
		types = append(types, names[i])
	}
	return parts, want.Bytes(), types
}

func gRequireNativeFrames(t *testing.T, got []byte, types []string, dataOnly bool) {
	t.Helper()
	requireValidResponsesSSE(t, got, len(types))
	frames := bytes.Split(bytes.TrimSuffix(got, []byte("\n\n")), []byte("\n\n"))
	for i, frame := range frames {
		var name string
		var data []byte
		for rest := frame; len(rest) > 0; {
			line, _, next := splitSSELine(rest)
			rest = next
			if bytes.HasPrefix(line, []byte("event: ")) {
				name = string(line[len("event: "):])
			}
			if bytes.HasPrefix(line, []byte("data:")) {
				data = sseFieldValue(line)
			}
		}
		var value struct {
			Type, Delta, Text string
			Response          struct {
				Model  string
				Output json.RawMessage
			}
		}
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if value.Type != types[i] || (!dataOnly && name != types[i]) || (dataOnly && name != "") {
			t.Fatalf("frame %d name=%q type=%q want=%q", i, name, value.Type, types[i])
		}
		switch value.Type {
		case "response.created", "response.in_progress", "response.completed":
			if value.Response.Model != "grok-4.6" {
				t.Fatalf("frame %d model=%q", i, value.Response.Model)
			}
		}
		if value.Type == "response.completed" && string(value.Response.Output) != gNativeOutput {
			t.Fatalf("completed output=%s", value.Response.Output)
		}
		if value.Type == "response.output_text.delta" && value.Delta != "ordinary grok-4.7 opaque 中文 output" {
			t.Fatalf("delta=%q", value.Delta)
		}
		if value.Type == "response.output_text.done" && value.Text != "ordinary grok-4.7 opaque 中文 output" {
			t.Fatalf("done text=%q", value.Text)
		}
	}
}

func TestFunctionalNativeSSEFieldSequence(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		for _, count := range []int{2, 9} {
			for _, finish := range []bool{false, true} {
				t.Run(fmt.Sprintf("dataOnly=%v/count=%d/finish=%v", dataOnly, count, finish), func(t *testing.T) {
					parts, want, types := gNativeFields(dataOnly, count)
					got := gNativeParts(t, "openai-response", finish, parts...)
					if !bytes.Equal(got, want) {
						t.Fatalf("native sequence bytes=%d want=%d, prefix=%q", len(got), len(want), got[:min(len(got), 250)])
					}
					gRequireNativeFrames(t, got, types, dataOnly)
				})
			}
		}
	}
}

func TestFunctionalNativeSSEFieldDataOnlyTerminalControl(t *testing.T) {
	for _, finish := range []bool{false, true} {
		parts, _, types := gNativeFields(true, 1)
		got := gNativeParts(t, "openai-response", finish, parts...)
		if string(bytes.TrimSuffix(got, []byte("\n\n"))) != "data: "+gNativeCompletedWant {
			t.Fatalf("single data-only control finish=%v output=%q", finish, got)
		}
		gRequireNativeFrames(t, got, types, true)
	}
}

func TestFunctionalNativeSSEFieldGenericCanonicalControls(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		parts, _, _ := gNativeFields(dataOnly, 2)
		for _, eol := range []string{"\n", "\r\n", "\r"} {
			t.Run(fmt.Sprintf("dataOnly=%v/eol=%q", dataOnly, eol), func(t *testing.T) {
				literal := append(bytes.Join(parts, nil), []byte(eol+eol)...)
				if got := gNativeParts(t, "claude", true, literal); !bytes.Equal(got, literal) {
					t.Fatalf("whole generic field changed: %q", got)
				}
				for split := 0; split <= len(literal); split++ {
					if got := gNativeParts(t, "claude", true, literal[:split], literal[split:]); !bytes.Equal(got, literal) {
						t.Fatalf("split=%d generic field changed: %q", split, got)
					}
				}
				bytewise := make([][]byte, len(literal))
				for i := range literal {
					bytewise[i] = literal[i : i+1]
				}
				if got := gNativeParts(t, "claude", true, bytewise...); !bytes.Equal(got, literal) {
					t.Fatalf("bytewise generic field changed: %q", got)
				}
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "claude", true
				for i, part := range parts {
					if chunks, err := r.Write(part); err != nil || len(chunks) != 0 {
						t.Fatalf("generic write=%d chunks=%q error=%v", i, chunks, err)
					}
				}
				chunks, err := r.Write([]byte(eol + eol))
				if err != nil {
					t.Fatal(err)
				}
				// 末尾 CR 仍可能组成 CRLF，Finish 确认该行结束符。
				flushed, err := r.Finish()
				got := bytes.Join(append(chunks, flushed...), nil)
				if err != nil || !bytes.Equal(got, literal) {
					t.Fatalf("generic delayed delimiter output=%q error=%v want=%q", got, err, literal)
				}
				if !dataOnly {
					line, _, remaining := splitSSELine(literal)
					if !bytes.HasPrefix(line, []byte("event:")) || hasSSEDataField(line) || hasSSEDataField(remaining) {
						t.Fatal("generic control must be one event field with zero data fields")
					}
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldDispatch(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		for _, count := range []int{2, 9} {
			t.Run(fmt.Sprintf("dataOnly=%v/count=%d", dataOnly, count), func(t *testing.T) {
				parts, want, types := gNativeFields(dataOnly, count)
				units := bytes.SplitAfter(want, []byte("\n\n"))[:count]
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				var got []byte
				for i, part := range parts {
					chunks, err := r.Write(part)
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, bytes.Join(chunks, nil)...)
					frames := (i + 1) / 2
					if dataOnly {
						frames = i
					}
					if expected := bytes.Join(units[:frames], nil); !bytes.Equal(got, expected) {
						t.Fatalf("write=%d want dataFrames=%d output=%q want=%q", i+1, frames, got, expected)
					}
				}
				chunks, err := r.Flush()
				got = append(got, bytes.Join(chunks, nil)...)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("flush output=%q error=%v want=%q", got, err, want)
				}
				gRequireNativeFrames(t, got, types, dataOnly)
			})
		}
	}
}

func TestFunctionalNativeSSEFieldLateDelimiter(t *testing.T) {
	parts, want, types := gNativeFields(false, 2)
	units := bytes.SplitAfter(want, []byte("\n\n"))[:2]
	for _, eol := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("eol=%q", eol), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			var got []byte
			for i, part := range parts {
				chunks, err := r.Write(part)
				got = append(got, bytes.Join(chunks, nil)...)
				if expected := bytes.Join(units[:(i+1)/2], nil); err != nil || !bytes.Equal(got, expected) {
					t.Fatalf("callback=%d cumulative dataFrames=%d output=%q error=%v want=%q", i+1, (i+1)/2, got, err, expected)
				}
			}
			chunks, err := r.Write([]byte(eol + eol))
			if err != nil || len(bytes.TrimSpace(bytes.Join(chunks, nil))) != 0 {
				t.Fatalf("late delimiter added output: chunks=%q error=%v", chunks, err)
			}
			flushed, err := r.Finish()
			if err != nil || len(bytes.TrimSpace(bytes.Join(flushed, nil))) != 0 || !bytes.Equal(got, want) {
				t.Fatalf("late delimiter finish=(%q,%v) data=%q", flushed, err, got)
			}
			gRequireNativeFrames(t, got, types, false)
		})
	}
}

func TestFunctionalNativeSSEFieldLateDataDelimiter(t *testing.T) {
	parts, want, types := gNativeFields(true, 2)
	first := bytes.SplitAfter(want, []byte("\n\n"))[0]
	for _, eol := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("eol=%q", eol), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			chunks, err := r.Write(parts[0])
			if err != nil || len(chunks) != 0 {
				t.Fatalf("first data=(%q,%v)", chunks, err)
			}
			chunks, err = r.Write(parts[1])
			got := bytes.Join(chunks, nil)
			if err != nil || !bytes.Equal(got, first) {
				t.Fatalf("next data dispatched=%q error=%v want=%q", got, err, first)
			}
			chunks, err = r.Write([]byte(eol + eol))
			got = append(got, bytes.Join(chunks, nil)...)
			flushed, flushErr := r.Flush()
			got = append(got, bytes.Join(flushed, nil)...)
			// delimiter 的行结束符可以保留；每个 data 单位及模型必须完整。
			normalized := bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
			if err != nil || flushErr != nil || !bytes.Equal(normalized, want) {
				t.Fatalf("late data delimiter output=%q write=%v flush=%v want=%q", got, err, flushErr, want)
			}
			gRequireNativeFrames(t, normalized, types, true)
		})
	}
}

func TestFunctionalNativeSSEFieldResponsesMetadataControl(t *testing.T) {
	parts, _, _ := gNativeFields(false, 2)
	for _, eol := range []string{"\n", "\r\n"} {
		literal := append(bytes.Join(parts, nil), []byte(eol+eol)...)
		if got := gNativeParts(t, "openai-response", true, literal); !bytes.Equal(got, literal) {
			t.Fatalf("whole event-only metadata changed: %q", got)
		}
		data := []byte("data: " + gNativeCompleted + eol + eol)
		want := append(bytes.Clone(literal), []byte("data: "+gNativeCompletedWant+eol+eol)...)
		if got := gNativeParts(t, "openai-response", true, literal, data); !bytes.Equal(got, want) {
			t.Fatalf("metadata then real terminal output=%q want=%q", got, want)
		}
	}
}

func gNativeMetadataCases() []struct {
	name                   string
	header, terminal, want []byte
} {
	parts, _, _ := gNativeFields(false, 2)
	joined := bytes.Join(parts, nil)
	resegmented := bytes.Join(parts[:3], nil)
	return []struct {
		name                   string
		header, terminal, want []byte
	}{
		{"host-no-LF", joined, bytes.Clone(parts[3]), append(bytes.Clone(joined), []byte("\ndata: "+gNativeCompletedWant+"\n\n")...)},
		{"resegmented", resegmented, append(bytes.Clone(parts[3]), '\n', '\n'), append(bytes.Clone(resegmented), []byte("\ndata: "+gNativeCompletedWant+"\n\n")...)},
		{"delimited-control", append(bytes.Clone(joined), '\n', '\n'), append(bytes.Clone(parts[3]), '\n', '\n'), append(bytes.Clone(joined), []byte("\n\ndata: "+gNativeCompletedWant+"\n\n")...)},
	}
}

func gRequireMetadataTerminal(t *testing.T, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("metadata/terminal output=%q want=%q", got, want)
	}
	// metadata 内嵌的 event/data/model 保持原文，仅检查真正的 data 行。
	var data []byte
	for rest := got; len(rest) > 0; {
		line, _, next := splitSSELine(rest)
		rest = next
		if bytes.HasPrefix(line, []byte("data:")) {
			if data != nil {
				t.Fatal("unexpected extra data field")
			}
			data = append(bytes.Clone(line), '\n', '\n')
		}
	}
	gRequireNativeFrames(t, data, []string{"response.completed"}, true)
}

func TestFunctionalNativeSSEFieldMetadataOutputUnits(t *testing.T) {
	for _, tc := range gNativeMetadataCases() {
		for _, finish := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/finish=%v", tc.name, finish), func(t *testing.T) {
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				var out [][]byte
				for _, input := range [][]byte{tc.header, tc.terminal} {
					part := bytes.Clone(input)
					chunks, err := r.Write(part)
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, chunks...)
					for i := range part {
						part[i] = 'z'
					}
				}
				var chunks [][]byte
				var err error
				if finish {
					chunks, err = r.Finish()
				} else {
					chunks, err = r.Flush()
				}
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, chunks...)
				gRequireMetadataTerminal(t, bytes.Join(out, nil), tc.want)
				if _, err := r.Write([]byte("data: {}\n\n")); err != nil {
					t.Fatal(err)
				}
				gRequireMetadataTerminal(t, bytes.Join(out, nil), tc.want)
			})
		}
	}
}

func TestFunctionalNativeSSEFieldMetadataForwarder(t *testing.T) {
	for _, tc := range gNativeMetadataCases() {
		for _, terminal := range []string{"natural", "done-payload", "in-band-error", "callback-error", "cleanup-errors", "emit-error"} {
			t.Run(tc.name+"/"+terminal, func(t *testing.T) {
				reads := []pluginapi.HostModelStreamReadResponse{{Payload: tc.header}, {Payload: tc.terminal}, {Done: true}}
				if terminal == "done-payload" {
					reads[1].Done = true
				}
				if terminal == "in-band-error" || terminal == "cleanup-errors" {
					reads[1].Error, reads[1].Done = "controlled upstream read error", true
				}
				readErr := errors.New("controlled callback read error")
				hostErr := errors.New("controlled host close error")
				pluginErr := errors.New("controlled plugin close error")
				emitErr := errors.New("controlled emit error")
				var emitted []byte
				var closeText string
				var order []string
				readCount, hostCloses, pluginCloses := 0, 0, 0
				stream := &executorStream{pluginStreamID: "g-metadata", hostStreamID: "g-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
				stream.call = func(method string, payload any) (json.RawMessage, error) {
					switch method {
					case pluginabi.MethodHostModelStreamRead:
						readCount++
						if terminal == "callback-error" && readCount == 3 {
							return nil, readErr
						}
						if len(reads) == 0 {
							return nil, errors.New("unexpected extra host read")
						}
						read := reads[0]
						reads = reads[1:]
						return json.Marshal(read)
					case pluginabi.MethodHostStreamEmit:
						order = append(order, "emit")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var emit struct{ Payload []byte }
						if err := json.Unmarshal(raw, &emit); err != nil {
							return nil, err
						}
						emitted = append(emitted, emit.Payload...)
						if terminal == "emit-error" && hasSSEDataField(emit.Payload) {
							return nil, emitErr
						}
					case pluginabi.MethodHostModelStreamClose:
						hostCloses++
						order = append(order, "host-close")
						if terminal == "cleanup-errors" {
							return nil, hostErr
						}
					case pluginabi.MethodHostStreamClose:
						pluginCloses++
						order = append(order, "plugin-close")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var closed struct{ Error string }
						if err := json.Unmarshal(raw, &closed); err != nil {
							return nil, err
						}
						closeText = closed.Error
						if terminal == "cleanup-errors" {
							return nil, pluginErr
						}
					default:
						return nil, fmt.Errorf("unexpected callback %s", method)
					}
					return json.RawMessage(`{}`), nil
				}
				err := runStreamForward(stream)
				gRequireMetadataTerminal(t, emitted, tc.want)
				wantOrder := "emit,host-close,plugin-close"
				if tc.name == "delimited-control" {
					wantOrder = "emit," + wantOrder
				}
				if terminal == "callback-error" {
					wantOrder = strings.TrimSuffix(wantOrder, ",plugin-close")
				}
				if strings.Join(order, ",") != wantOrder || hostCloses != 1 {
					t.Fatalf("order=%v host closes=%d want=%s", order, hostCloses, wantOrder)
				}
				switch terminal {
				case "callback-error":
					if !errors.Is(err, readErr) || pluginCloses != 0 || readCount != 3 {
						t.Fatalf("error=%v plugin closes=%d reads=%d", err, pluginCloses, readCount)
					}
				case "cleanup-errors":
					if !errors.Is(err, hostErr) || !errors.Is(err, pluginErr) || !strings.Contains(closeText, "controlled upstream read error") || !strings.Contains(closeText, hostErr.Error()) || pluginCloses != 1 || readCount != 2 {
						t.Fatalf("error=%v close=%q plugin closes=%d reads=%d", err, closeText, pluginCloses, readCount)
					}
				default:
					wantError, wantReads := "", 2
					if terminal == "natural" {
						wantReads = 3
					} else if terminal == "in-band-error" {
						wantError = "controlled upstream read error"
					} else if terminal == "emit-error" {
						wantError = "emit stream chunk: " + emitErr.Error()
					}
					if err != nil || closeText != wantError || pluginCloses != 1 || readCount != wantReads {
						t.Fatalf("error=%v close=%q plugin closes=%d reads=%d", err, closeText, pluginCloses, readCount)
					}
				}
			})
		}
	}
}

func gNativeLargeFixture(size int, dataOnly bool) ([][]byte, []byte) {
	value := `{"type":"response.output_text.done","response":{"model":"grok-4.7"},"text":"` + strings.Repeat("x", size) + `","opaque":{"text":"grok-4.7 中文","n":1.00}}`
	wantValue := `{"type":"response.output_text.done","response":{"model":"grok-4.6"},"text":"` + strings.Repeat("x", size) + `","opaque":{"text":"grok-4.7 中文","n":1.00}}`
	data := []byte("data: " + value)
	var parts [][]byte
	want := "data: " + wantValue + "\n\n"
	if !dataOnly {
		parts = append(parts, []byte("event: response.output_text.done"))
		want = "event: response.output_text.done\n" + want
	}
	for start := 0; start < len(data); start += 8 << 10 {
		parts = append(parts, data[start:min(start+(8<<10), len(data))])
	}
	return parts, []byte(want)
}

func gNativeContinue(parts [][]byte) ([][]byte, error) {
	r := newStreamChunkRewriter("grok-4.6")
	r.format, r.frameRawJSONAsSSE = "openai-response", true
	var chunks [][]byte
	for _, part := range parts {
		out, err := r.Write(part)
		chunks = append(chunks, out...)
		if err != nil {
			return chunks, err
		}
	}
	out, err := r.Finish()
	return append(chunks, out...), err
}

func TestFunctionalNativeSSEFieldContinuation(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		for _, size := range []int{2 << 20, 8 << 20} {
			t.Run(fmt.Sprintf("dataOnly=%v/bytes=%d", dataOnly, size), func(t *testing.T) {
				parts, want := gNativeLargeFixture(size, dataOnly)
				chunks, err := gNativeContinue(parts)
				got := bytes.Join(chunks, nil)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatalf("native continuation error=%v bytes=%d want=%d", err, len(got), len(want))
				}
				for _, part := range parts {
					for i := range part {
						part[i] = 'z'
					}
				}
				if !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatal("native chunks alias input bytes")
				}
				frozen := bytes.Clone(got)
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				var previous [][]byte
				fresh, _ := gNativeLargeFixture(size, dataOnly)
				for _, part := range fresh {
					out, writeErr := r.Write(part)
					if writeErr != nil {
						t.Fatal(writeErr)
					}
					previous = append(previous, out...)
				}
				out, finishErr := r.Finish()
				if finishErr != nil {
					t.Fatal(finishErr)
				}
				previous = append(previous, out...)
				if _, writeErr := r.Write([]byte("data: {}\n\n")); writeErr != nil {
					t.Fatal(writeErr)
				}
				if !bytes.Equal(bytes.Join(previous, nil), frozen) {
					t.Fatal("native chunks changed after future write")
				}
			})
		}
	}
}

func BenchmarkStreamChunkRewriterNativeFieldContinuation(b *testing.B) {
	for _, dataOnly := range []bool{false, true} {
		for _, size := range []int{2 << 20, 8 << 20} {
			b.Run(fmt.Sprintf("dataOnly=%v/bytes=%d/fragment=8192", dataOnly, size), func(b *testing.B) {
				parts, want := gNativeLargeFixture(size, dataOnly)
				chunks, err := gNativeContinue(parts)
				got := bytes.Join(chunks, nil)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					b.Fatalf("byte-exact native preflight error=%v bytes=%d want=%d", err, len(got), len(want))
				}
				input := bytes.Join(parts, nil)
				for _, part := range parts {
					for i := range part {
						part[i] = 'z'
					}
				}
				if !bytes.Equal(bytes.Join(chunks, nil), got) {
					b.Fatal("native preflight output aliases input")
				}
				for offset, i := 0, 0; i < len(parts); i++ {
					copy(parts[i], input[offset:offset+len(parts[i])])
					offset += len(parts[i])
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(want)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					chunks, err = gNativeContinue(parts)
					outputBytes := 0
					for _, chunk := range chunks {
						outputBytes += len(chunk)
					}
					if err != nil || outputBytes != len(got) {
						b.Fatalf("native continuation=(%d bytes,%v), want %d bytes", outputBytes, err, len(got))
					}
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldContinuationAllocations(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("dataOnly=%v", dataOnly), func(t *testing.T) {
			parts, want := gNativeLargeFixture(2<<20, dataOnly)
			chunks, err := gNativeContinue(parts)
			if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
				t.Fatalf("native allocation preflight=(%d,%v)", len(chunks), err)
			}
			allocations := testing.AllocsPerRun(1, func() {
				out, err := gNativeContinue(parts)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(out, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					panic(fmt.Sprintf("native allocation output=(%d,%v)", len(out), err))
				}
			})
			if allocations > 200 {
				t.Fatalf("native continuation allocations=%v want <=200", allocations)
			}
		})
	}
}

func TestFunctionalNativeSSEFieldInputOwnership(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("dataOnly=%v", dataOnly), func(t *testing.T) {
			parts, want, _ := gNativeFields(dataOnly, 2)
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			var chunks [][]byte
			for _, part := range parts {
				out, err := r.Write(part)
				if err != nil {
					t.Fatal(err)
				}
				chunks = append(chunks, out...)
				for i := range part {
					part[i] = 'z'
				}
			}
			out, err := r.Finish()
			if err != nil {
				t.Fatal(err)
			}
			chunks = append(chunks, out...)
			if got := bytes.Join(chunks, nil); !bytes.Equal(got, want) {
				t.Fatalf("native input ownership output=%q want=%q", got, want)
			}
			frozen := bytes.Join(chunks, nil)
			if _, err := r.Write([]byte("data: {}\n\n")); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.Join(chunks, nil), frozen) {
				t.Fatal("previous native output changed after future Write")
			}
		})
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataOutputUnits(t *testing.T) {
	for _, metadata := range [][]string{nil, {"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
		for _, dataOnly := range []bool{false, true} {
			for _, count := range []int{1, 9} {
				for _, finish := range []bool{false, true} {
					t.Run(fmt.Sprintf("metadata=%q/dataOnly=%v/count=%d/finish=%v", metadata, dataOnly, count, finish), func(t *testing.T) {
						parts, want, types := gNativeFields(dataOnly, count)
						var prefix []byte
						var inputs [][]byte
						for _, field := range metadata {
							inputs = append(inputs, []byte(field))
							prefix = append(prefix, []byte(field+"\n")...)
						}
						inputs = append(inputs, parts...)
						want = append(prefix, want...)
						r := newStreamChunkRewriter("grok-4.6")
						r.format, r.frameRawJSONAsSSE = "openai-response", true
						var chunks [][]byte
						for _, field := range inputs {
							out, err := r.Write(field)
							if err != nil {
								t.Fatal(err)
							}
							chunks = append(chunks, out...)
							for i := range field {
								field[i] = 'z'
							}
						}
						var out [][]byte
						var err error
						if finish {
							out, err = r.Finish()
						} else {
							out, err = r.Flush()
						}
						if err != nil {
							t.Fatal(err)
						}
						chunks = append(chunks, out...)
						got := bytes.Join(chunks, nil)
						comparable := got
						if dataOnly && count == 1 {
							comparable, want = bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))
						}
						if !bytes.Equal(comparable, want) {
							t.Fatalf("metadata output=%q want=%q", got, want)
						}
						gRequireNativeFrames(t, got, types, dataOnly)
						frozen := bytes.Clone(got)
						if _, err := r.Write([]byte("data: {}\n\n")); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(bytes.Join(chunks, nil), frozen) {
							t.Fatal("previous metadata output changed after future Write")
						}
					})
				}
			}
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataPositions(t *testing.T) {
	for _, metadata := range [][]string{{"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
		for _, dataOnly := range []bool{false, true} {
			for _, count := range []int{1, 9} {
				for _, position := range []string{"before-event", "in-event", "between-events"} {
					t.Run(fmt.Sprintf("metadata=%q/dataOnly=%v/count=%d/%s", metadata, dataOnly, count, position), func(t *testing.T) {
						parts, wire, types := gNativeFields(dataOnly, count)
						units := bytes.SplitAfter(wire, []byte("\n\n"))[:count]
						var input [][]byte
						var want []byte
						stride := 2
						if dataOnly {
							stride = 1
						}
						for i, unit := range units {
							prefix := []byte(strings.Join(metadata, "\n") + "\n")
							if position == "between-events" && i == 0 {
								prefix = nil
							}
							fields := parts[i*stride : (i+1)*stride]
							if position == "in-event" && !dataOnly {
								input = append(input, fields[0])
								for _, field := range metadata {
									input = append(input, []byte(field))
								}
								input = append(input, fields[1])
								line, rest, ok := bytes.Cut(unit, []byte("\n"))
								if !ok {
									t.Fatal("fixture has no event line")
								}
								want = append(want, line...)
								want = append(want, '\n')
								want = append(want, prefix...)
								want = append(want, rest...)
							} else {
								if len(prefix) > 0 {
									for _, field := range metadata {
										input = append(input, []byte(field))
									}
								}
								input = append(input, fields...)
								want = append(want, prefix...)
								want = append(want, unit...)
							}
						}
						got := gNativeParts(t, "openai-response", true, input...)
						comparable := got
						if dataOnly && count == 1 {
							comparable, want = bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))
						}
						if !bytes.Equal(comparable, want) {
							t.Fatalf("position output=%q want=%q", got, want)
						}
						gRequireNativeFrames(t, got, types, dataOnly)
					})
				}
			}
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataWire(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		wire := []byte("id: event-1" + eol + "retry: 100" + eol + `: opaque event: response.created data: {"model":"grok-4.7"} 中文` + eol + "event: response.completed" + eol + "data: " + gNativeCompleted + eol + eol)
		want := bytes.Replace(wire, []byte("data: "+gNativeCompleted), []byte("data: "+gNativeCompletedWant), 1)
		for split := 0; split <= len(wire); split++ {
			if got := gNativeParts(t, "claude", true, wire[:split], wire[split:]); !bytes.Equal(got, want) {
				t.Fatalf("eol=%q split=%d got=%q want=%q", eol, split, got, want)
			}
		}
		parts := make([][]byte, len(wire))
		for i := range wire {
			parts[i] = wire[i : i+1]
		}
		if got := gNativeParts(t, "claude", true, parts...); !bytes.Equal(got, want) {
			t.Fatalf("eol=%q bytewise output=%q want=%q", eol, got, want)
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataForwarder(t *testing.T) {
	for _, metadata := range []string{"id: event-1", "retry: 100", ": heartbeat"} {
		for _, terminal := range []string{"natural", "done-payload", "in-band-error", "callback-error", "cleanup-errors", "emit-error"} {
			t.Run(metadata+"/"+terminal, func(t *testing.T) {
				reads := []pluginapi.HostModelStreamReadResponse{{Payload: []byte(metadata)}, {Payload: []byte("event: response.completed")}, {Payload: []byte("data: " + gNativeCompleted)}, {Done: true}}
				if terminal == "done-payload" {
					reads[2].Done = true
				}
				if terminal == "in-band-error" || terminal == "cleanup-errors" {
					reads[2].Error, reads[2].Done = "controlled upstream read error", true
				}
				readErr, hostErr, pluginErr, emitErr := errors.New("controlled callback read error"), errors.New("controlled host close error"), errors.New("controlled plugin close error"), errors.New("controlled emit error")
				var output []byte
				var order []string
				var closeText string
				readCount, hostCloses, pluginCloses := 0, 0, 0
				stream := &executorStream{pluginStreamID: "metadata-draft", hostStreamID: "metadata-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
				stream.call = func(method string, payload any) (json.RawMessage, error) {
					switch method {
					case pluginabi.MethodHostModelStreamRead:
						readCount++
						if terminal == "callback-error" && readCount == 4 {
							return nil, readErr
						}
						if len(reads) == 0 {
							return nil, errors.New("unexpected extra read")
						}
						next := reads[0]
						reads = reads[1:]
						return json.Marshal(next)
					case pluginabi.MethodHostStreamEmit:
						order = append(order, "emit")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var emit struct{ Payload []byte }
						if err := json.Unmarshal(raw, &emit); err != nil {
							return nil, err
						}
						output = append(output, emit.Payload...)
						if terminal == "emit-error" && hasSSEDataField(emit.Payload) {
							return nil, emitErr
						}
					case pluginabi.MethodHostModelStreamClose:
						hostCloses++
						order = append(order, "host-close")
						if terminal == "cleanup-errors" {
							return nil, hostErr
						}
					case pluginabi.MethodHostStreamClose:
						pluginCloses++
						order = append(order, "plugin-close")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var closed struct{ Error string }
						if err := json.Unmarshal(raw, &closed); err != nil {
							return nil, err
						}
						closeText = closed.Error
						if terminal == "cleanup-errors" {
							return nil, pluginErr
						}
					default:
						return nil, fmt.Errorf("unexpected method %s", method)
					}
					return json.RawMessage(`{}`), nil
				}
				err := runStreamForward(stream)
				want := []byte(metadata + "\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
				if !bytes.Equal(output, want) {
					t.Errorf("metadata forwarder=%q want=%q", output, want)
				}
				if hostCloses != 1 || len(order) == 0 {
					t.Fatalf("order=%v host closes=%d", order, hostCloses)
				}
				wantCloses := "host-close,plugin-close"
				if terminal == "callback-error" {
					wantCloses = "host-close"
				}
				var closes []string
				for _, action := range order {
					if action != "emit" {
						closes = append(closes, action)
					}
				}
				if strings.Join(closes, ",") != wantCloses || order[0] != "emit" {
					t.Errorf("order=%v want emit before %s", order, wantCloses)
				}
				switch terminal {
				case "callback-error":
					if !errors.Is(err, readErr) || pluginCloses != 0 || readCount != 4 {
						t.Errorf("error=%v plugin closes=%d reads=%d", err, pluginCloses, readCount)
					}
				case "cleanup-errors":
					if !errors.Is(err, hostErr) || !errors.Is(err, pluginErr) || !strings.Contains(closeText, "controlled upstream read error") || !strings.Contains(closeText, hostErr.Error()) || pluginCloses != 1 || readCount != 3 {
						t.Errorf("error=%v close=%q plugin closes=%d reads=%d", err, closeText, pluginCloses, readCount)
					}
				default:
					wantError, wantReads := "", 3
					if terminal == "natural" {
						wantReads = 4
					} else if terminal == "in-band-error" {
						wantError = "controlled upstream read error"
					} else if terminal == "emit-error" {
						wantError = "emit stream chunk: " + emitErr.Error()
					}
					if err != nil || closeText != wantError || pluginCloses != 1 || readCount != wantReads {
						t.Errorf("error=%v close=%q plugin closes=%d reads=%d", err, closeText, pluginCloses, readCount)
					}
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataDispatch(t *testing.T) {
	for _, metadata := range [][]string{{"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
		for _, dataOnly := range []bool{false, true} {
			for _, eol := range []string{"\n", "\r\n"} {
				t.Run(fmt.Sprintf("metadata=%q/dataOnly=%v/eol=%q", metadata, dataOnly, eol), func(t *testing.T) {
					r := newStreamChunkRewriter("grok-4.6")
					r.format, r.frameRawJSONAsSSE = "openai-response", true
					parts, want, types := gNativeFields(dataOnly, 2)
					want = append([]byte(strings.Join(metadata, "\n")+"\n"), want...)
					var got []byte
					for _, field := range metadata {
						out, err := r.Write([]byte(field))
						got = append(got, bytes.Join(out, nil)...)
						if err != nil || bytes.Contains(got, []byte("\ndata:")) {
							t.Fatalf("metadata dispatched data=%q error=%v", got, err)
						}
					}
					for i, part := range parts {
						out, err := r.Write(part)
						got = append(got, bytes.Join(out, nil)...)
						frames := (i + 1) / 2
						if dataOnly {
							frames = i
						}
						if err != nil || bytes.Count(got, []byte("\ndata: ")) != frames {
							t.Fatalf("callback=%d dataFrames=%d want=%d output=%q error=%v", i+1, bytes.Count(got, []byte("\ndata: ")), frames, got, err)
						}
					}
					out, err := r.Write([]byte(eol + eol))
					if err != nil {
						t.Fatal(err)
					}
					if dataOnly {
						got = append(got, bytes.Join(out, nil)...)
					} else if len(bytes.TrimSpace(bytes.Join(out, nil))) != 0 {
						t.Fatalf("late delimiter added event=%q", out)
					}
					out, err = r.Finish()
					if dataOnly {
						got = append(got, bytes.Join(out, nil)...)
					} else if len(bytes.TrimSpace(bytes.Join(out, nil))) != 0 {
						t.Fatalf("Finish added event=%q", out)
					}
					if err != nil || !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
						t.Fatalf("late delimiter output=%q error=%v want=%q", got, err, want)
					}
					gRequireNativeFrames(t, bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), types, dataOnly)
				})
			}
		}
	}
}

func gNativeOrdinaryMetadataLargeFixture(size int, dataOnly bool) ([][]byte, []byte) {
	parts, want := gNativeLargeFixture(size, dataOnly)
	metadata := [][]byte{[]byte("id: event-1"), []byte("retry: 100"), []byte(`: opaque event: response.created data: {"model":"grok-4.7"} 中文`)}
	prefix := append(bytes.Join(metadata, []byte("\n")), '\n')
	return append(metadata, parts...), append(prefix, want...)
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataContinuation(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		for _, size := range []int{2 << 20, 8 << 20} {
			t.Run(fmt.Sprintf("dataOnly=%v/bytes=%d", dataOnly, size), func(t *testing.T) {
				parts, want := gNativeOrdinaryMetadataLargeFixture(size, dataOnly)
				chunks, err := gNativeContinue(parts)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatalf("continuation error=%v", err)
				}
				if size == 2<<20 {
					allocs := testing.AllocsPerRun(1, func() {
						out, err := gNativeContinue(parts)
						if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(out, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
							panic(fmt.Sprintf("allocation preflight error=%v", err))
						}
					})
					if allocs > 200 {
						t.Fatalf("allocations=%v want<=200", allocs)
					}
				}
				for _, part := range parts {
					for i := range part {
						part[i] = 'z'
					}
				}
				if !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatal("metadata continuation aliases input")
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataPendingLimit(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("dataOnly=%v", dataOnly), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			metadata := []byte("id: event-1")
			if _, err := r.Write(metadata); err != nil {
				t.Fatal(err)
			}
			var header []byte
			if !dataOnly {
				header = []byte("event: response.output_text.done")
				if _, err := r.Write(header); err != nil {
					t.Fatal(err)
				}
			}
			data := []byte(`data: {"type":"response.output_text.done","text":"` + strings.Repeat("x", maxPendingStreamBytes) + `"}`)
			cut := maxPendingStreamBytes - len(header)
			if chunks, err := r.Write(data[:cut]); err != nil || len(chunks) != 0 {
				t.Fatalf("metadata must not count against incomplete data: chunks=%d error=%v", len(chunks), err)
			}
			chunks, err := r.Write(data[cut:])
			flushed, flushErr := r.Finish()
			want := append(bytes.Clone(metadata), '\n')
			if !dataOnly {
				want = append(want, header...)
				want = append(want, '\n')
			}
			want = append(want, data...)
			if err != nil || flushErr != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(append(chunks, flushed...), nil), []byte("\n\n")), want) {
				t.Fatalf("complete continuation error=%v flush=%v", err, flushErr)
			}
		})
	}
}

func BenchmarkStreamChunkRewriterOrdinaryMetadataContinuation(b *testing.B) {
	for _, dataOnly := range []bool{false, true} {
		for _, size := range []int{2 << 20, 8 << 20} {
			b.Run(fmt.Sprintf("dataOnly=%v/bytes=%d/fragment=8192", dataOnly, size), func(b *testing.B) {
				parts, want := gNativeOrdinaryMetadataLargeFixture(size, dataOnly)
				chunks, err := gNativeContinue(parts)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					b.Fatalf("byte-exact metadata preflight error=%v", err)
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(want)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					chunks, err = gNativeContinue(parts)
					if err != nil || len(chunks) == 0 {
						b.Fatalf("continuation=(%d,%v)", len(chunks), err)
					}
				}
			})
		}
	}
}

// 移除 mixed 单位的边界派发或把 metadata 拼进 JSON，会使这些真实 output-unit 回归失败。
func hNativeMixedFields(count int, firstDataOnly bool, metadata []string, position string) ([][]byte, []byte) {
	names, payloads := gNativeSequence()
	indices := []int{0, 8}
	if count == 9 {
		indices = []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	}
	var parts [][]byte
	var want bytes.Buffer
	for n, i := range indices {
		dataOnly := firstDataOnly != (n%2 != 0)
		addMetadata := func() {
			for _, field := range metadata {
				parts = append(parts, []byte(field))
				fmt.Fprintln(&want, field)
			}
		}
		if position == "before-event" || position == "between-events" && n > 0 {
			addMetadata()
		}
		if !dataOnly {
			parts = append(parts, []byte("event: "+names[i]))
			fmt.Fprintln(&want, "event: "+names[i])
		}
		if position == "in-event" {
			addMetadata()
		}
		parts = append(parts, []byte("data: "+payloads[i]))
		restored := payloads[i]
		if i < 2 || i == 8 {
			restored = strings.Replace(restored, `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1)
		}
		fmt.Fprint(&want, "data: "+restored+"\n\n")
	}
	return parts, want.Bytes()
}

func TestFunctionalNativeSSEFieldMixedMetadata(t *testing.T) {
	for _, metadata := range [][]string{nil, {"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
		for _, firstDataOnly := range []bool{true, false} {
			for _, count := range []int{2, 9} {
				for _, position := range []string{"before-event", "in-event", "between-events"} {
					for _, finish := range []bool{false, true} {
						t.Run(fmt.Sprintf("metadata=%q/firstDataOnly=%v/count=%d/%s/finish=%v", metadata, firstDataOnly, count, position, finish), func(t *testing.T) {
							parts, want := hNativeMixedFields(count, firstDataOnly, metadata, position)
							got := gNativeParts(t, "openai-response", finish, parts...)
							if !bytes.Equal(got, want) {
								t.Fatalf("mixed output=%q want=%q", got, want)
							}
							requireValidResponsesSSE(t, got, count)
						})
					}
				}
			}
		}
	}
}

func TestFunctionalNativeSSEFieldMixedMetadataDispatch(t *testing.T) {
	for _, metadata := range [][]string{{"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque data: {"model":"grok-4.7"} 中文`}} {
		for _, eol := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("metadata=%q/eol=%q", metadata, eol), func(t *testing.T) {
				parts, want := hNativeMixedFields(2, true, metadata, "between-events")
				first, _, ok := bytes.Cut(want, []byte("\n\n"))
				if !ok {
					t.Fatal("fixture has no first event")
				}
				first = append(bytes.Clone(first), '\n', '\n')
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				var chunks [][]byte
				for i, part := range parts {
					out, err := r.Write(part)
					if err != nil {
						t.Fatal(err)
					}
					chunks = append(chunks, out...)
					got := bytes.Join(chunks, nil)
					switch {
					case i <= len(metadata):
						if len(got) != 0 {
							t.Fatalf("metadata dispatched data at %d: %q", i, got)
						}
					case i == len(metadata)+1:
						if !bytes.Equal(got, first) {
							t.Fatalf("event callback output=%q want=%q", got, first)
						}
					default:
						if !bytes.Equal(got, want) {
							t.Fatalf("data callback output=%q want=%q", got, want)
						}
					}
					for j := range part {
						part[j] = 'z'
					}
				}
				frozen := bytes.Clone(bytes.Join(chunks, nil))
				out, err := r.Write([]byte(eol + eol))
				if err != nil || len(bytes.TrimSpace(bytes.Join(out, nil))) != 0 {
					t.Fatalf("late delimiter=(%q,%v)", out, err)
				}
				out, err = r.Finish()
				if err != nil || len(bytes.TrimSpace(bytes.Join(out, nil))) != 0 {
					t.Fatalf("finish=(%q,%v)", out, err)
				}
				if _, err := r.Write([]byte("data: {}\n\n")); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(bytes.Join(chunks, nil), frozen) || !bytes.Equal(frozen, want) {
					t.Fatal("mixed output aliases input or future buffer")
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldMixedMetadataWireControls(t *testing.T) {
	metadata := []string{"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}
	for _, firstDataOnly := range []bool{true, false} {
		parts, want := hNativeMixedFields(2, firstDataOnly, metadata, "between-events")
		for _, format := range []string{"openai", "claude", "gemini", "interactions"} {
			if got := gNativeParts(t, format, true, parts...); !bytes.Equal(got, bytes.Join(parts, nil)) {
				t.Fatalf("inactive format=%s changed mixed units", format)
			}
		}
		wire := bytes.ReplaceAll(want, []byte(`"model":"grok-4.6"`), []byte(`"model":"grok-4.7"`))
		for _, eol := range []string{"\n", "\r", "\r\n"} {
			input := bytes.ReplaceAll(wire, []byte("\n"), []byte(eol))
			expected := bytes.ReplaceAll(want, []byte("\n"), []byte(eol))
			for _, bom := range []bool{false, true} {
				if bom {
					input = append([]byte{0xef, 0xbb, 0xbf}, input...)
				}
				for split := 0; split <= len(input); split++ {
					got := gNativeParts(t, "claude", true, input[:split], input[split:])
					if !bytes.Equal(got, expected) {
						t.Fatalf("generic eol=%q bom=%v split=%d got=%q want=%q", eol, bom, split, got, expected)
					}
				}
				var bytewise [][]byte
				for i := range input {
					bytewise = append(bytewise, input[i:i+1])
				}
				if got := gNativeParts(t, "claude", true, bytewise...); !bytes.Equal(got, expected) {
					t.Fatal("generic bytewise differs")
				}
			}
		}
	}
}

func hNativeMixedLargeFixture(size int) ([][]byte, []byte) {
	parts, want := hNativeMixedFields(2, true, []string{"id: event-1", "retry: 100", ": heartbeat"}, "between-events")
	large, largeWant := gNativeLargeFixture(size, false)
	// 小 created 与 metadata 后续写大 field-pair，沿用单个大单位的 allocation 条件。
	prefixEnd := bytes.Index(want, []byte("event: response.completed"))
	return append(parts[:len(parts)-2], large...), append(want[:prefixEnd:prefixEnd], largeWant...)
}

func TestFunctionalNativeSSEFieldMixedMetadataContinuation(t *testing.T) {
	for _, size := range []int{2 << 20, 8 << 20, maxPendingStreamBytes} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			parts, want := hNativeMixedLargeFixture(size)
			if size == maxPendingStreamBytes {
				// 最后一次 Write 补完仍在限额内的 pending，不先提交越界的 incomplete 单位。
				data := bytes.Join(parts[5:], nil)
				cut := maxPendingStreamBytes - len(parts[4])
				parts = append(parts[:5:5], data[:cut], data[cut:])
			}
			chunks, err := gNativeContinue(parts)
			if err != nil || !bytes.Equal(bytes.Join(chunks, nil), want) {
				t.Fatalf("mixed large bytes=%d want=%d error=%v", len(bytes.Join(chunks, nil)), len(want), err)
			}
			if size == 2<<20 {
				allocs := testing.AllocsPerRun(1, func() {
					out, err := gNativeContinue(parts)
					if err != nil || !bytes.Equal(bytes.Join(out, nil), want) {
						panic(fmt.Sprint(err))
					}
				})
				if allocs > 200 {
					t.Fatalf("mixed allocations=%v want<=200", allocs)
				}
			}
			for _, part := range parts {
				for i := range part {
					part[i] = 'z'
				}
			}
			if !bytes.Equal(bytes.Join(chunks, nil), want) {
				t.Fatal("mixed large output aliases input")
			}
		})
	}
}

func TestFunctionalNativeSSEFieldMixedMetadataPrefixBeforeError(t *testing.T) {
	parts, want := hNativeMixedFields(2, true, []string{"id: event-1"}, "between-events")
	r := newStreamChunkRewriter("grok-4.6")
	r.format, r.frameRawJSONAsSSE = "openai-response", true
	var out []byte
	for _, part := range parts {
		chunks, err := r.Write(part)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, bytes.Join(chunks, nil)...)
	}
	chunks, err := r.Write([]byte(`data: {"type":"response.output_text.done","text":"` + strings.Repeat("x", maxPendingStreamBytes)))
	out = append(out, bytes.Join(chunks, nil)...)
	if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") || !bytes.Equal(out, want) {
		t.Fatalf("prefix bytes=%d error=%v want=%d", len(out), err, len(want))
	}
	chunks, err = r.Flush()
	if err != nil || len(chunks) != 0 {
		t.Fatalf("cleared tail flush=(%q,%v)", chunks, err)
	}
}

func BenchmarkStreamChunkRewriterMixedMetadataContinuation(b *testing.B) {
	for _, size := range []int{2 << 20, 8 << 20} {
		b.Run(fmt.Sprintf("bytes=%d/fragment=8192", size), func(b *testing.B) {
			parts, want := hNativeMixedLargeFixture(size)
			chunks, err := gNativeContinue(parts)
			if err != nil || !bytes.Equal(bytes.Join(chunks, nil), want) {
				b.Fatalf("byte-exact mixed preflight error=%v", err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(want)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				chunks, err = gNativeContinue(parts)
				if err != nil || len(chunks) == 0 {
					b.Fatalf("mixed continuation=(%d,%v)", len(chunks), err)
				}
			}
		})
	}
}

func TestFunctionalNativeSSEFieldMixedMetadataLargeFirstControl(t *testing.T) {
	parts, want := hNativeMixedFields(2, true, []string{"id: event-1", "retry: 100", ": heartbeat"}, "between-events")
	large, largeWant := gNativeLargeFixture(2<<20, true)
	parts = append(large, parts[1:]...)
	want = append(largeWant, want[bytes.Index(want, []byte("\n\n"))+2:]...)
	got := gNativeParts(t, "openai-response", true, parts...)
	if !bytes.Equal(got, want) {
		t.Fatalf("large first mixed bytes=%d want=%d", len(got), len(want))
	}
}

func TestFunctionalNativeSSEFieldMixedMetadataCursorAndTraffic(t *testing.T) {
	parts, want := hNativeMixedLargeFixture(2 << 20)
	r := newStreamChunkRewriter("grok-4.6")
	r.format, r.frameRawJSONAsSSE = "openai-response", true
	for repeat := 0; repeat < 9; repeat++ {
		var output []byte
		previous := 0
		for i, part := range parts {
			out, err := r.Write(part)
			if err != nil {
				t.Fatal(err)
			}
			output = append(output, bytes.Join(out, nil)...)
			if i >= 5 && len(r.sse.buf) > 0 {
				s := r.sse.delimiterless
				if s.disabled || s.jsonScan != len(r.sse.buf) || s.jsonScan <= previous || s.headerScan >= s.jsonScan {
					t.Fatalf("continuation cursor=%+v previous=%d bytes=%d", s, previous, len(r.sse.buf))
				}
				previous = s.jsonScan
			}
		}
		out, err := r.Flush()
		output = append(output, bytes.Join(out, nil)...)
		if err != nil || !bytes.Equal(output, want) {
			t.Fatalf("complete cumulative traffic repeat=%d bytes=%d error=%v", repeat, len(output), err)
		}
	}
}

func TestFunctionalNativeSSEBoundaryMetadata(t *testing.T) {
	for _, field := range []struct{ name, value string }{{"bare-id", "id"}, {"bare-retry", "retry"}, {"extension", "x-request-id: trace-123"}} {
		t.Run(field.name, func(t *testing.T) {
			got := gNativeParts(t, "openai-response", true, []byte(field.value), []byte("event: response.completed"), []byte("data: "+gNativeCompleted))
			want := []byte(field.value + "\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("native fields=%q want=%q", got, want)
			}
			gRequireNativeFrames(t, got, []string{"response.completed"}, false)
		})
	}
}

func TestFunctionalNativeSSEBoundaryEmptyFields(t *testing.T) {
	for _, field := range []struct{ name, value string }{{"bare-data", "data"}, {"bare-event", "event"}, {"empty-event", "event:"}, {"empty-event-space", "event: "}, {"codex-empty-data", "data:"}, {"codex-empty-data-space", "data: "}} {
		t.Run(field.name, func(t *testing.T) {
			got := gNativeParts(t, "openai-response", true, []byte(field.value), []byte("event: response.completed"), []byte("data: "+gNativeCompleted))
			want := []byte(field.value + "\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("empty fields=%q want=%q", got, want)
			}
			gRequireNativeFrames(t, got, []string{"response.completed"}, false)
		})
	}
}

func TestFunctionalNativeSSEBoundaryContinuations(t *testing.T) {
	for _, field := range []string{"id: cursor", "retry: 100", "x-request-id: trace-123", "false-field: opaque event: response.created data: grok-4.7 中文"} {
		for _, bytewise := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bytewise=%v", field, bytewise), func(t *testing.T) {
				var parts [][]byte
				if bytewise {
					for i := 0; i < len(field); i++ {
						parts = append(parts, []byte(field[i:i+1]))
					}
				} else {
					name, value, ok := strings.Cut(field, ":")
					if !ok {
						t.Fatal("fixture missing colon")
					}
					parts = [][]byte{[]byte(name), []byte(":" + value)}
				}
				parts = append(parts, []byte("\nevent: response.completed\ndata: "+gNativeCompleted+"\n\n"))
				want := []byte(field + "\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
				if got := gNativeParts(t, "openai-response", true, parts...); !bytes.Equal(got, want) {
					t.Fatalf("continued field=%q want=%q", got, want)
				}
			})
		}
	}
}

func gBoundaryFields(fields []string, position, shape string, count int) ([][]byte, []string, string) {
	names, payloads := gNativeSequence()
	indices := []int{8}
	if count == 2 {
		indices = []int{0, 8}
	} else if count == 9 {
		indices = []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	}
	var parts [][]byte
	var after []string
	var emitted, pending string
	write := func(part string) {
		parts = append(parts, []byte(part))
		after = append(after, emitted)
	}
	for n, i := range indices {
		dataOnly := shape == "data-only" || shape == "alternating-data" && n%2 == 0 || shape == "alternating-pair" && n%2 == 1
		unit := ""
		hasHeader := !dataOnly
		metadata := func() {
			for _, field := range fields {
				if strings.HasPrefix(field, "event:") && strings.TrimSpace(strings.TrimPrefix(field, "event:")) != "" {
					emitted += pending
					pending = ""
					hasHeader = true
				}
				write(field)
				unit += field + "\n"
			}
		}
		if position == "before-event" || position == "between-events" && n > 0 {
			metadata()
		}
		if !dataOnly {
			emitted += pending
			pending = ""
			write("event: " + names[i])
			unit += "event: " + names[i] + "\n"
		}
		if position == "in-event" {
			metadata()
		}
		emitted += pending
		pending = ""
		unit += "data: " + strings.Replace(payloads[i], `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1) + "\n\n"
		if !hasHeader {
			pending = unit
		} else {
			emitted += unit
		}
		write("data: " + payloads[i])
	}
	return parts, after, emitted + pending
}

func TestFunctionalNativeSSEBoundaryDispatch(t *testing.T) {
	for _, fields := range [][]string{{"id"}, {"retry"}, {"x-request-id: trace-123"}, {"data"}, {"event"}, {"event:"}, {"event: "}, {"data:"}, {"data: "}, {"id", "retry", "event:", "event: response.completed", "event:"}} {
		for _, position := range []string{"before-event", "in-event", "between-events"} {
			for _, shape := range []string{"pair", "data-only", "alternating-data", "alternating-pair"} {
				for _, count := range []int{1, 2, 9} {
					t.Run(fmt.Sprintf("fields=%q/%s/%s/count=%d", fields, position, shape, count), func(t *testing.T) {
						parts, after, want := gBoundaryFields(fields, position, shape, count)
						r := newStreamChunkRewriter("grok-4.6")
						r.format, r.frameRawJSONAsSSE = "openai-response", true
						var got []byte
						for i, part := range parts {
							out, err := r.Write(part)
							got = append(got, bytes.Join(out, nil)...)
							if err != nil || string(got) != after[i] {
								t.Fatalf("Write[%d]=%q cumulative=%q want=%q error=%v", i, part, got, after[i], err)
							}
						}
						out, err := r.Finish()
						got = append(got, bytes.Join(out, nil)...)
						if err != nil || string(got) != want {
							t.Fatalf("Finish=%q want=%q error=%v", got, want, err)
						}
						requireValidResponsesSSE(t, got, count)
						for _, delimiter := range []string{"\n\n", "\r\n\r\n"} {
							out, err := r.Write([]byte(delimiter))
							if err != nil || len(bytes.TrimSpace(bytes.Join(out, nil))) != 0 {
								t.Fatalf("late delimiter=%q output=%q error=%v", delimiter, out, err)
							}
						}
						for _, finish := range []func() ([][]byte, error){r.Flush, r.Finish, r.Finish} {
							out, err := finish()
							if err != nil || len(bytes.TrimSpace(bytes.Join(out, nil))) != 0 {
								t.Fatalf("repeat finish=%q error=%v", out, err)
							}
						}
					})
				}
			}
		}
	}
}

func TestFunctionalNativeSSEBoundaryContinuationFields(t *testing.T) {
	for _, field := range []string{"id: cursor", "retry: 100", "x-request-id: trace-123"} {
		t.Run("native-follow/"+field, func(t *testing.T) {
			name, value, ok := strings.Cut(field, ":")
			if !ok {
				t.Fatal("fixture missing colon")
			}
			got := gNativeParts(t, "openai-response", true, []byte(name), []byte(":"+value), []byte("event: response.completed"), []byte("data: "+gNativeCompleted))
			want := field + "\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n"
			if string(got) != want {
				t.Fatalf("native continuation=%q want=%q", got, want)
			}
		})
	}
	for _, field := range []string{"event: response.completed", "data: " + gNativeCompleted, "x-request-id: trace-123"} {
		for _, cut := range []int{1, len(strings.Split(field, ":")[0]), len(field) - 1} {
			t.Run(fmt.Sprintf("field=%s/cut=%d", strings.Split(field, ":")[0], cut), func(t *testing.T) {
				parts := [][]byte{[]byte(field[:cut]), nil, []byte(field[cut:])}
				want := ""
				switch {
				case strings.HasPrefix(field, "event:"):
					parts = append(parts, []byte("\ndata: "+gNativeCompleted+"\n\n"))
					want = field + "\ndata: " + gNativeCompletedWant + "\n\n"
				case strings.HasPrefix(field, "data:"):
					parts = append(parts, []byte("\n\n"))
					want = "data: " + gNativeCompletedWant + "\n\n"
				default:
					parts = append(parts, []byte("\nevent: response.completed\ndata: "+gNativeCompleted+"\n\n"))
					want = field + "\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n"
				}
				if got := gNativeParts(t, "openai-response", true, parts...); string(got) != want {
					t.Fatalf("continued=%q want=%q", got, want)
				}
			})
		}
	}
}

func TestFunctionalNativeSSEBoundaryEmptyEventTerminal(t *testing.T) {
	for _, typ := range []string{"error", "keepalive"} {
		for _, empty := range []string{"event", "event:", "event: "} {
			t.Run(typ+"/"+empty, func(t *testing.T) {
				data := `{"type":"` + typ + `","opaque":{"n":1.00,"text":"grok-4.7 中文"}}`
				parts := [][]byte{[]byte("event: " + typ), []byte(empty), []byte("data: " + data), []byte("event: response.completed"), []byte("data: " + gNativeCompleted)}
				want := []byte("event: " + typ + "\n" + empty + "\ndata: " + data + "\n\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
				for _, finish := range []bool{false, true} {
					if got := gNativeParts(t, "openai-response", finish, parts...); !bytes.Equal(got, want) {
						t.Fatalf("empty event terminal=%q want=%q finish=%v", got, want, finish)
					}
				}
			})
		}
	}
}

func TestFunctionalNativeSSEBoundaryCommentCallback(t *testing.T) {
	for _, field := range []string{"event:", "event: ", "data:", "data: ", "x-request-id: trace-123"} {
		t.Run(field, func(t *testing.T) {
			got := gNativeParts(t, "openai-response", true, []byte(field), []byte(": heartbeat"), []byte("event: response.completed"), []byte("data: "+gNativeCompleted))
			want := []byte(field + "\n: heartbeat\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("comment callback=%q want=%q", got, want)
			}
			gRequireNativeFrames(t, got, []string{"response.completed"}, false)
		})
	}
}

func TestFunctionalNativeSSEBoundaryCompleteComment(t *testing.T) {
	for _, field := range []string{"id", "retry", "x-request-id: trace-123", "data", "event:", "data: "} {
		t.Run(field, func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			for _, part := range []string{"data: " + gNativeCompleted, field} {
				if out, err := r.Write([]byte(part)); err != nil || len(out) != 0 {
					t.Fatalf("pending=%q output=%q error=%v", part, out, err)
				}
			}
			out, err := r.Write([]byte(": keepalive\n\n"))
			want := "data: " + gNativeCompletedWant + "\n\n" + field + "\n: keepalive\n\n"
			if err != nil || string(bytes.Join(out, nil)) != want {
				t.Fatalf("complete comment=%q want=%q error=%v", out, want, err)
			}
			if out, err := r.Finish(); err != nil || len(out) != 0 {
				t.Fatalf("finish=%q error=%v", out, err)
			}
		})
	}
}

func TestFunctionalNativeSSEBoundaryAmbiguity(t *testing.T) {
	for _, tc := range gNativeMetadataCases()[:2] {
		t.Run("metadata-prefix/"+tc.name, func(t *testing.T) {
			want := append([]byte("id\n"), tc.want...)
			if got := gNativeParts(t, "openai-response", true, []byte("id"), tc.header, tc.terminal); !bytes.Equal(got, want) {
				t.Fatalf("opaque native header=%q want=%q", got, want)
			}
		})
	}
	for _, parts := range [][][]byte{
		{[]byte("id")}, {[]byte("retry")}, {[]byte("event:")}, {[]byte("data: ")}, {[]byte("x-request-id: trace-123")},
		{[]byte("id"), []byte("event: response.completed"), []byte(`data: {"type":"response.created","model":"grok-4.7"}`)},
		{[]byte("x-request-id: event: response.completed data: " + gNativeCompleted)},
	} {
		if got, want := gNativeParts(t, "openai-response", true, parts...), bytes.Join(parts, nil); !bytes.Equal(got, want) {
			t.Fatalf("unresolved=%q output=%q want=%q", parts, got, want)
		}
	}
	for _, field := range []string{"true-field: opaque event: response.created data: grok-4.7 中文", "false-field: trace", "null-field: trace", "1-field: trace", "-field: trace"} {
		got := gNativeParts(t, "openai-response", true, []byte(field), []byte("event: response.completed"), []byte("data: "+gNativeCompleted))
		want := []byte(field + "\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
		if !bytes.Equal(got, want) {
			t.Fatalf("extension=%q output=%q want=%q", field, got, want)
		}
	}
}

func TestFunctionalNativeSSEBoundaryForwarder(t *testing.T) {
	for _, field := range []string{"id", "retry", "x-request-id: trace-123", "data", "event", "event:", "data: "} {
		for _, terminal := range []string{"natural", "done-payload", "in-band-error", "callback-error", "decode-error", "cleanup-errors", "emit-error"} {
			t.Run(field+"/"+terminal, func(t *testing.T) {
				reads := []pluginapi.HostModelStreamReadResponse{{Payload: []byte(field)}, {Payload: []byte("data: " + gNativeCompleted)}, {Done: true}}
				if terminal == "done-payload" {
					reads[1].Done = true
				} else if terminal == "in-band-error" || terminal == "cleanup-errors" {
					reads[1].Done, reads[1].Error = true, "original boundary error"
				}
				readErr, hostErr, pluginErr, emitErr := errors.New("boundary callback error"), errors.New("boundary host close error"), errors.New("boundary plugin close error"), errors.New("boundary emit error")
				var got []byte
				var order []string
				closeText := ""
				readCount, hostCloses, pluginCloses := 0, 0, 0
				stream := &executorStream{pluginStreamID: "boundary", hostStreamID: "boundary-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
				stream.call = func(method string, payload any) (json.RawMessage, error) {
					switch method {
					case pluginabi.MethodHostModelStreamRead:
						readCount++
						if readCount == 3 && terminal == "callback-error" {
							return nil, readErr
						}
						if readCount == 3 && terminal == "decode-error" {
							return json.RawMessage(`{"payload":`), nil
						}
						if len(reads) == 0 {
							return nil, errors.New("unexpected extra boundary read")
						}
						read := reads[0]
						reads = reads[1:]
						return json.Marshal(read)
					case pluginabi.MethodHostStreamEmit:
						order = append(order, "emit")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var emit struct{ Payload []byte }
						if err := json.Unmarshal(raw, &emit); err != nil {
							return nil, err
						}
						got = append(got, emit.Payload...)
						if terminal == "emit-error" {
							return nil, emitErr
						}
					case pluginabi.MethodHostModelStreamClose:
						hostCloses++
						order = append(order, "host-close")
						if terminal == "cleanup-errors" {
							return nil, hostErr
						}
					case pluginabi.MethodHostStreamClose:
						pluginCloses++
						order = append(order, "plugin-close")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var closed struct{ Error string }
						if err := json.Unmarshal(raw, &closed); err != nil {
							return nil, err
						}
						closeText = closed.Error
						if terminal == "cleanup-errors" {
							return nil, pluginErr
						}
					default:
						return nil, fmt.Errorf("unexpected callback %s", method)
					}
					return json.RawMessage(`{}`), nil
				}
				err := runStreamForward(stream)
				want := field + "\ndata: " + gNativeCompletedWant + "\n\n"
				wantOrder := "emit,host-close,plugin-close"
				wantReads := 3
				if terminal == "done-payload" || terminal == "in-band-error" || terminal == "cleanup-errors" {
					wantReads = 2
				}
				if terminal == "callback-error" || terminal == "decode-error" {
					wantOrder = "emit,host-close"
				}
				if string(got) != want || strings.Join(order, ",") != wantOrder || readCount != wantReads || hostCloses != 1 || pluginCloses != strings.Count(wantOrder, "plugin-close") {
					t.Fatalf("forwarded=%q want=%q order=%v want=%s reads=%d want=%d closes=%d/%d", got, want, order, wantOrder, readCount, wantReads, hostCloses, pluginCloses)
				}
				switch terminal {
				case "callback-error":
					if !errors.Is(err, readErr) {
						t.Fatal(err)
					}
				case "decode-error":
					if err == nil || !strings.Contains(err.Error(), "decode host stream chunk") {
						t.Fatal(err)
					}
				case "cleanup-errors":
					if !errors.Is(err, hostErr) || !errors.Is(err, pluginErr) || !strings.Contains(closeText, "original boundary error") || !strings.Contains(closeText, hostErr.Error()) {
						t.Fatalf("error=%v close=%s", err, closeText)
					}
				default:
					wantError := ""
					if terminal == "in-band-error" {
						wantError = "original boundary error"
					} else if terminal == "emit-error" {
						wantError = "emit flushed stream chunk: boundary emit error"
					}
					if err != nil || closeText != wantError {
						t.Fatalf("error=%v close=%s want=%s", err, closeText, wantError)
					}
				}
			})
		}
	}
}

func TestFunctionalNativeSSEBoundaryRPC(t *testing.T) {
	for _, terminal := range []string{"natural", "done-payload", "error-done", "emit-error"} {
		t.Run(terminal, func(t *testing.T) {
			resetExecutorStreamLifecycle()
			setLoadedConfigForTest(Config{GlobalRules: "grok-4.6=>grok-4.7"})
			t.Cleanup(func() {
				shutdownExecutorStreams()
				resetExecutorStreamLifecycle()
				setHostCallbackForTest(nil)
				setLoadedConfigForTest(defaultConfig())
			})
			var mu sync.Mutex
			var output []byte
			var order []string
			var closeText string
			reads, hostCloses, pluginCloses := 0, 0, 0
			closed := make(chan struct{})
			respond := func(payload any) ([]byte, error) {
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				return json.Marshal(pluginabi.Envelope{OK: true, Result: raw})
			}
			setHostCallbackForTest(func(method string, request []byte) ([]byte, error) {
				mu.Lock()
				defer mu.Unlock()
				switch method {
				case pluginabi.MethodHostModelExecuteStream:
					var req pluginapi.HostModelExecutionRequest
					if err := json.Unmarshal(request, &req); err != nil {
						return nil, err
					}
					if req.Model != "grok-4.7" || string(req.Body) != `{"model":"grok-4.7","input":"grok-4.6"}` || req.Headers.Get("X-Boundary") != "preserved" || req.ExitProtocol != "openai-response" || !req.Stream {
						return nil, fmt.Errorf("outbound request=%+v", req)
					}
					return respond(pluginapi.HostModelStreamResponse{StreamID: "rpc-boundary-host", StatusCode: 200, Headers: http.Header{"Content-Type": {"text/event-stream"}, "X-Boundary": {"response"}}})
				case pluginabi.MethodHostModelStreamRead:
					reads++
					chunk := pluginapi.HostModelStreamReadResponse{}
					switch reads {
					case 1:
						chunk.Payload = []byte("id")
					case 2:
						chunk.Payload = []byte("data: " + gNativeCompleted)
						chunk.Done = terminal == "done-payload" || terminal == "error-done"
						if terminal == "error-done" {
							chunk.Error = "original RPC terminal error"
						}
					case 3:
						chunk.Done = true
					default:
						return nil, errors.New("unexpected extra RPC read")
					}
					return respond(chunk)
				case pluginabi.MethodHostStreamEmit:
					var emit struct {
						StreamID string `json:"stream_id"`
						Payload  []byte `json:"payload"`
					}
					if err := json.Unmarshal(request, &emit); err != nil || emit.StreamID != "rpc-boundary" {
						return nil, fmt.Errorf("emit=%s error=%v", request, err)
					}
					output = append(output, emit.Payload...)
					order = append(order, "emit")
					if terminal == "emit-error" {
						return json.Marshal(pluginabi.Envelope{OK: false, Error: &pluginabi.Error{Code: "test_emit", Message: "RPC emit failed"}})
					}
					return respond(struct{}{})
				case pluginabi.MethodHostModelStreamClose:
					hostCloses++
					order = append(order, "host-close")
					return respond(struct{}{})
				case pluginabi.MethodHostStreamClose:
					var value struct{ Error string }
					if err := json.Unmarshal(request, &value); err != nil {
						return nil, err
					}
					closeText = value.Error
					pluginCloses++
					order = append(order, "plugin-close")
					close(closed)
					return respond(struct{}{})
				default:
					return nil, fmt.Errorf("unexpected RPC callback=%s", method)
				}
			})
			req := executorRPCRequest{StreamID: "rpc-boundary", Model: "grok-4.6", Format: "openai-response", SourceFormat: "openai-response", OriginalRequest: []byte(`{"model":"grok-4.6","input":"grok-4.6"}`), Headers: http.Header{"X-Boundary": {"preserved"}}}
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			response, err := handleMethod(pluginabi.MethodExecutorExecuteStream, raw)
			var envelope pluginabi.Envelope
			if err != nil || json.Unmarshal(response, &envelope) != nil || !envelope.OK {
				t.Fatalf("RPC response=%s error=%v", response, err)
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("RPC stream did not close")
			}
			executorStreamLifecycle.wg.Wait()
			mu.Lock()
			defer mu.Unlock()
			wantError, wantReads := "", 3
			if terminal == "done-payload" || terminal == "error-done" {
				wantReads = 2
			}
			if terminal == "error-done" {
				wantError = "original RPC terminal error"
			} else if terminal == "emit-error" {
				wantError = "emit flushed stream chunk: RPC emit failed"
			}
			if string(output) != "id\ndata: "+gNativeCompletedWant+"\n\n" || closeText != wantError || reads != wantReads || hostCloses != 1 || pluginCloses != 1 || strings.Join(order, ",") != "emit,host-close,plugin-close" {
				t.Fatalf("RPC output=%q close=%q reads=%d closes=%d/%d order=%v", output, closeText, reads, hostCloses, pluginCloses, order)
			}
		})
	}
}

func TestFunctionalNativeSSEFieldMixedMetadataWhitelist(t *testing.T) {
	created := `{"type":"response.created","model":"grok-4.7","modelVersion":"grok-4.7","response":{"model":"grok-4.7","modelVersion":"grok-4.7"},"message":{"model":"grok-4.7"},"interaction":{"model":"grok-4.7"},"opaque":{"model":"grok-4.7","text":"event: data: grok-4.7 中文"}}`
	wantCreated := `{"type":"response.created","model":"grok-4.6","modelVersion":"grok-4.6","response":{"model":"grok-4.6","modelVersion":"grok-4.6"},"message":{"model":"grok-4.6"},"interaction":{"model":"grok-4.6"},"opaque":{"model":"grok-4.7","text":"event: data: grok-4.7 中文"}}`
	got := gNativeParts(t, "openai-response", true, []byte("data: "+created), []byte("id: event-1"), []byte("event: response.completed"), []byte("data: "+gNativeCompleted))
	want := []byte("data: " + wantCreated + "\n\nid: event-1\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("mixed whitelist=%q want=%q", got, want)
	}
}

func TestFunctionalNativeSSEFieldMixedMetadataForwarder(t *testing.T) {
	for _, terminal := range []string{"natural", "done-payload", "in-band-error", "callback-error"} {
		t.Run(terminal, func(t *testing.T) {
			parts, want := hNativeMixedFields(2, true, []string{"id: event-1"}, "between-events")
			var reads []pluginapi.HostModelStreamReadResponse
			for _, p := range parts {
				reads = append(reads, pluginapi.HostModelStreamReadResponse{Payload: p})
			}
			reads = append(reads, pluginapi.HostModelStreamReadResponse{Done: true})
			if terminal == "done-payload" {
				reads[len(reads)-2].Done = true
			}
			if terminal == "in-band-error" {
				reads[len(reads)-2].Error = "controlled mixed read error"
				reads[len(reads)-2].Done = true
			}
			var output []byte
			var closeText string
			hostCloses, pluginCloses := 0, 0
			readErr := errors.New("controlled mixed callback error")
			stream := &executorStream{pluginStreamID: "mixed-forwarder", hostStreamID: "mixed-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
			stream.call = func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostModelStreamRead:
					if terminal == "callback-error" && len(reads) == 1 {
						return nil, readErr
					}
					if len(reads) == 0 {
						return nil, errors.New("unexpected extra mixed read")
					}
					read := reads[0]
					reads = reads[1:]
					return json.Marshal(read)
				case pluginabi.MethodHostStreamEmit:
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var emit struct{ Payload []byte }
					if err := json.Unmarshal(raw, &emit); err != nil {
						return nil, err
					}
					output = append(output, emit.Payload...)
				case pluginabi.MethodHostModelStreamClose:
					hostCloses++
				case pluginabi.MethodHostStreamClose:
					pluginCloses++
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var value struct{ Error string }
					if err := json.Unmarshal(raw, &value); err != nil {
						return nil, err
					}
					closeText = value.Error
				default:
					return nil, fmt.Errorf("unexpected method %s", method)
				}
				return json.RawMessage(`{}`), nil
			}
			err := runStreamForward(stream)
			if !bytes.Equal(output, want) || hostCloses != 1 {
				t.Fatalf("mixed forwarder output=%q hostCloses=%d want=%q", output, hostCloses, want)
			}
			if terminal == "callback-error" {
				if !errors.Is(err, readErr) || pluginCloses != 0 {
					t.Fatalf("callback error=%v pluginCloses=%d", err, pluginCloses)
				}
			} else {
				expected := ""
				if terminal == "in-band-error" {
					expected = "controlled mixed read error"
				}
				if err != nil || pluginCloses != 1 || closeText != expected {
					t.Fatalf("terminal error=%v closes=%d closeText=%q", err, pluginCloses, closeText)
				}
			}
		})
	}
}
