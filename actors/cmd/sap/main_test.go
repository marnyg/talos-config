package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSSE: the stream parser handles id/event/data framing, keepalive
// comments, multi-line data and the terminal `end` event.
func TestSSE(t *testing.T) {
	in := ": keepalive\n\n" +
		"id: 1\nevent: line\ndata: {\"seq\":1,\"type\":\"a\"}\n\n" +
		"id: 2\nevent: line\ndata: {\"seq\":2,\ndata: \"type\":\"b\"}\n\n" +
		"event: end\ndata: {}\n\n"
	var got []string
	ended, err := sse(strings.NewReader(in), func(name string, data []byte) {
		got = append(got, name+":"+string(data))
	})
	if err != nil || !ended {
		t.Fatalf("ended=%v err=%v", ended, err)
	}
	want := []string{`line:{"seq":1,"type":"a"}`, "line:{\"seq\":2,\n\"type\":\"b\"}"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	// A stream that just closes is not ended.
	if ended, _ := sse(strings.NewReader("id: 1\nevent: line\ndata: {}\n\n"), func(string, []byte) {}); ended {
		t.Fatal("closed stream reported ended")
	}
}

// TestLoginAndBearer: `sap login` against a fake parent — the browser
// step is the fake's /cli/authorize fetching the callback itself; the
// token is saved 0600 and then presented as a bearer to /api/me.
func TestLoginAndBearer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SAP_SERVER", "")
	t.Setenv("SAP_TOKEN", "")
	const token = "abc.def"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/authorize":
			q := r.URL.Query()
			// The "browser" approves at once.
			go http.Get(fmt.Sprintf("http://127.0.0.1:%s/?state=%s&token=%s", q.Get("port"), q.Get("state"), token))
		case "/api/me":
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(principal{Wallet: "0xabc", Username: "me", Exp: time.Now().Add(time.Hour).Unix()})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(fake.Close)

	browse = func(u string) { go http.Get(u) }
	t.Cleanup(func() { browse = openBrowser })
	if err := login(context.Background(), config{}, []string{"-server", fake.URL}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "sap", "config.json")
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config: %v %v", err, st)
	}
	cfg := loadConfig()
	if cfg.Server != fake.URL || cfg.Token != token {
		t.Fatalf("config: %+v", cfg)
	}
	c := &client{server: cfg.Server, token: cfg.Token, http: fake.Client()}
	var me principal
	if err := c.get(context.Background(), "/api/me", false, &me); err != nil || me.Wallet != "0xabc" {
		t.Fatalf("me: %v %+v", err, me)
	}
	// A stale token is the readable error.
	c.token = "stale"
	if err := c.get(context.Background(), "/api/me", false, &me); err == nil || !strings.Contains(err.Error(), "sap login") {
		t.Fatalf("stale: %v", err)
	}
}

// TestNewTask: `sap new` posts the UI's create body — project,
// title from the rest of the positionals, only the flags given, and
// start — and prints the task the parent returns (201).
func TestNewTask(t *testing.T) {
	var got map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/tasks" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(task{ID: "t1", Title: got["title"].(string), Flow: "default", State: "framed"})
	}))
	t.Cleanup(fake.Close)
	c := &client{server: fake.URL, token: "tok", http: fake.Client()}
	if err := newTask(context.Background(), c, []string{"test", "Add", "hello.txt", "-repo", "agents/scratch", "-start", "-desc", "one line"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"project": "test", "title": "Add hello.txt", "repo": "agents/scratch", "start": true, "description": "one line"}
	if len(got) != len(want) {
		t.Fatalf("body %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("body[%s] = %v, want %v", k, got[k], v)
		}
	}
}

// TestWatchRenders: watch against a fake stream prints the rendered
// lines and returns at `end`; -raw prints one line per event.
func TestWatchRenders(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/stream") {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		ev := func(seq int, typ, raw string) {
			b, _ := json.Marshal(event{Seq: int64(seq), At: 1700000000, Type: typ, Raw: json.RawMessage(raw)})
			fmt.Fprintf(w, "id: %d\nevent: line\ndata: %s\n\n", seq, b)
		}
		ev(1, "message_end", `{"message":{"role":"user","content":"do the thing"}}`)
		ev(2, "message_update", `{"assistantMessageEvent":{"type":"text_delta","delta":"x"}}`)
		ev(3, "message_end", `{"message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"toolCall","name":"bash","arguments":{"cmd":"ls"}}]}}`)
		ev(4, "tool_execution_end", `{"toolName":"bash","isError":false,"result":{"content":[{"type":"text","text":"a\nb"}]}}`)
		ev(5, "child_settled", `{"type":"child_settled","status":"settled","events":4,"pr":"https://x/pr/1"}`)
		fmt.Fprint(w, "event: end\ndata: {}\n\n")
	}))
	t.Cleanup(fake.Close)

	out := capture(t, func() {
		c := &client{server: fake.URL, token: "t", http: fake.Client()}
		if err := watch(context.Background(), c, []string{"s1"}); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"user:\n  do the thing", "assistant:\n  on it", "▸ bash", "✓ bash — a", "settled: settled events=4 pr=https://x/pr/1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "text_delta") {
		t.Error("deltas rendered")
	}
	raw := capture(t, func() {
		c := &client{server: fake.URL, token: "t", http: fake.Client()}
		watch(context.Background(), c, []string{"s1", "-raw"})
	})
	if strings.Count(raw, "event ") != 5 || !strings.HasPrefix(raw, "event 1 message_end ") {
		t.Errorf("raw:\n%s", raw)
	}
}

func capture(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	f()
	w.Close()
	os.Stdout = old
	return <-done
}
