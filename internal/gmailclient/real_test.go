package gmailclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gclean/internal/defang"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func TestNewRealClient_MissingPath(t *testing.T) {
	_, err := NewRealClient("")
	if err != ErrCredentialsMissing {
		t.Fatalf("want ErrCredentialsMissing, got %v", err)
	}
}

func TestNewRealClient_MissingCredentials(t *testing.T) {
	tmp := t.TempDir()
	_, err := NewRealClient(tmp + "/nonexistent.json")
	if err == nil {
		t.Fatal("want error for missing credentials, got nil")
	}
}

func TestMapGmailMessage_CombinesToAndCcRecipients(t *testing.T) {
	message := mapGmailMessage(&gmail.Message{
		Id:           "m1",
		InternalDate: 1_700_000_000_000,
		Payload: &gmail.MessagePart{Headers: []*gmail.MessagePartHeader{
			{Name: "From", Value: defang.MkEmail("sender", "example.com")},
			{Name: "To", Value: "first@example.com, second@example.com"},
			{Name: "Cc", Value: " third@example.com "},
		}},
	})

	want := []string{"first@example.com", "second@example.com", "third@example.com"}
	if len(message.To) != len(want) {
		t.Fatalf("got %d recipients, want %d: %v", len(message.To), len(want), message.To)
	}
	for i, recipient := range want {
		if message.To[i] != recipient {
			t.Errorf("recipient %d = %q, want %q", i, message.To[i], recipient)
		}
	}
}

func TestRealClient_TrashMessages_RetriesTransientErrors(t *testing.T) {
	stubRetryDelay(t, func(int, error) time.Duration { return 0 })
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gmail/v1/users/me/messages/m1/trash" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "try again", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	client := newHTTPTestClient(t, server)
	if err := client.TrashMessages([]string{"m1"}); err != nil {
		t.Fatalf("TrashMessages: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("request attempts = %d, want 2", got)
	}
}

// stubRetryDelay replaces the package-level retryDelay with a test stub and
// restores it when the test finishes.
func stubRetryDelay(t *testing.T, fn func(attempt int, err error) time.Duration) {
	t.Helper()
	original := retryDelay
	retryDelay = fn
	t.Cleanup(func() { retryDelay = original })
}

func TestRealClient_TrashMessages_HonorsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch attempts.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
		case 2:
			w.Header().Set("Retry-After", "2")
			http.Error(w, "still slow down", http.StatusTooManyRequests)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()

	// Record the computed waits via the real defaultRetryDelay but never
	// actually sleep, keeping the test fast and deterministic.
	var waits []time.Duration
	stubRetryDelay(t, func(attempt int, err error) time.Duration {
		d := defaultRetryDelay(attempt, err)
		waits = append(waits, d)
		return 0
	})

	client := newHTTPTestClient(t, server)
	if err := client.TrashMessages([]string{"m1"}); err != nil {
		t.Fatalf("TrashMessages: %v", err)
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if len(waits) != len(want) {
		t.Fatalf("recorded waits = %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("recorded waits = %v, want %v (Retry-After not honored at attempt %d)", waits, want, i+1)
		}
	}
}

func TestRetryAfterDelay_Seconds(t *testing.T) {
	err := &googleapi.Error{Code: 429, Header: http.Header{"Retry-After": []string{"5"}}}
	d, ok := retryAfterDelay(err)
	if !ok || d != 5*time.Second {
		t.Fatalf("retryAfterDelay = %v, %v; want 5s, true", d, ok)
	}
}

func TestRetryAfterDelay_HTTPDate(t *testing.T) {
	future := time.Now().Add(30 * time.Second).UTC()
	err := &googleapi.Error{Code: 429, Header: http.Header{"Retry-After": []string{future.Format(http.TimeFormat)}}}
	d, ok := retryAfterDelay(err)
	if !ok {
		t.Fatal("want parsed HTTP-date Retry-After")
	}
	if d < 25*time.Second || d > 30*time.Second {
		t.Fatalf("retryAfterDelay = %v, want ~30s", d)
	}
}

func TestRetryAfterDelay_Capped(t *testing.T) {
	err := &googleapi.Error{Code: 429, Header: http.Header{"Retry-After": []string{"3600"}}}
	d, ok := retryAfterDelay(err)
	if !ok || d != maxRetryAfterWait {
		t.Fatalf("retryAfterDelay = %v, %v; want %v, true", d, ok, maxRetryAfterWait)
	}
}

func TestRetryAfterDelay_MissingOrNonAPIError(t *testing.T) {
	if _, ok := retryAfterDelay(&googleapi.Error{Code: 429}); ok {
		t.Fatal("want no Retry-After without header")
	}
	if _, ok := retryAfterDelay(errors.New("boom")); ok {
		t.Fatal("want no Retry-After for non-googleapi error")
	}
}

func TestDefaultRetryDelay_ExponentialWithJitter(t *testing.T) {
	cases := []struct {
		attempt int
		lo, hi  time.Duration
	}{
		{attempt: 1, lo: time.Second, hi: 2 * time.Second},
		{attempt: 2, lo: 2 * time.Second, hi: 3 * time.Second},
		{attempt: 3, lo: 4 * time.Second, hi: 5 * time.Second},
		{attempt: 6, lo: backoffCap, hi: backoffCap + time.Second},
		{attempt: 10, lo: backoffCap, hi: backoffCap + time.Second},
	}
	for _, tc := range cases {
		d := defaultRetryDelay(tc.attempt, errors.New("transient"))
		if d < tc.lo || d >= tc.hi {
			t.Errorf("attempt %d: delay = %v, want [%v, %v)", tc.attempt, d, tc.lo, tc.hi)
		}
	}
}

func TestRealClient_RestoreFromTrash_UsesUntrash(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	if err := newHTTPTestClient(t, server).RestoreFromTrash([]string{"m1"}); err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if gotPath != "/gmail/v1/users/me/messages/m1/untrash" {
		t.Fatalf("path = %q, want untrash endpoint", gotPath)
	}
}

func TestRealClient_InTrash_DetectsTrashLabel(t *testing.T) {
	stubRetryDelay(t, func(int, error) time.Duration { return 0 })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/gmail/v1/users/me/messages/m1" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"m1","labelIds":["INBOX","TRASH"]}`)
	}))
	defer server.Close()

	in, err := newHTTPTestClient(t, server).InTrash([]string{"m1"})
	if err != nil {
		t.Fatalf("InTrash: %v", err)
	}
	if len(in) != 1 || in[0] != "m1" {
		t.Fatalf("InTrash = %v, want [m1]", in)
	}
}

func TestRealClient_InTrash_ExcludesNonTrashed(t *testing.T) {
	stubRetryDelay(t, func(int, error) time.Duration { return 0 })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"m2","labelIds":["INBOX"]}`)
	}))
	defer server.Close()

	in, err := newHTTPTestClient(t, server).InTrash([]string{"m2"})
	if err != nil {
		t.Fatalf("InTrash: %v", err)
	}
	if len(in) != 0 {
		t.Fatalf("InTrash = %v, want empty", in)
	}
}

func TestRealClient_EmptyTrash_FallsBackToIndividualDeleteOn403(t *testing.T) {
	stubRetryDelay(t, func(int, error) time.Duration { return 0 })
	var (
		batchCalls atomic.Int32
		deleted    []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gmail/v1/users/me/messages":
			response := map[string]any{"messages": []map[string]string{{"id": "t1"}, {"id": "t2"}, {"id": "t3"}}}
			_ = json.NewEncoder(w).Encode(response)
		case r.Method == http.MethodPost && r.URL.Path == "/gmail/v1/users/me/messages/batchDelete":
			batchCalls.Add(1)
			// Google's backend rejects batchDelete without the full-access
			// scope even when the token holds gmail.modify.
			http.Error(w, "Request had insufficient authentication scopes.", http.StatusForbidden)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/gmail/v1/users/me/messages/"):
			deleted = append(deleted, path.Base(r.URL.Path))
			_, _ = fmt.Fprint(w, `{}`)
		default:
			http.Error(w, "unexpected request: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	if err := newHTTPTestClient(t, server).EmptyTrash(); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if batchCalls.Load() != 1 {
		t.Fatalf("batchDelete calls = %d, want 1", batchCalls.Load())
	}
	want := []string{"t1", "t2", "t3"}
	if strings.Join(deleted, ",") != strings.Join(want, ",") {
		t.Fatalf("individually deleted = %v, want %v", deleted, want)
	}
}

func TestRealClient_EmptyTrash_BatchesAllPages(t *testing.T) {
	const total = mutationBatchSize + 1
	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gmail/v1/users/me/messages":
			if got := r.URL.Query().Get("labelIds"); got != "TRASH" {
				t.Errorf("labelIds = %q, want TRASH", got)
			}
			start, end := 0, mutationBatchSize
			if r.URL.Query().Get("pageToken") == "next" {
				start, end = mutationBatchSize, total
			}
			messages := make([]map[string]string, end-start)
			for i := range messages {
				messages[i] = map[string]string{"id": "trash-" + strconv.Itoa(start+i)}
			}
			response := map[string]any{"messages": messages}
			if start == 0 {
				response["nextPageToken"] = "next"
			}
			_ = json.NewEncoder(w).Encode(response)
		case r.Method == http.MethodPost && r.URL.Path == "/gmail/v1/users/me/messages/batchDelete":
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode batch body: %v", err)
				return
			}
			batchSizes = append(batchSizes, len(body.IDs))
			_, _ = fmt.Fprint(w, `{}`)
		default:
			http.Error(w, "unexpected request: "+path.Base(r.URL.Path), http.StatusNotFound)
		}
	}))
	defer server.Close()

	if err := newHTTPTestClient(t, server).EmptyTrash(); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if got, want := strings.TrimSpace(fmt.Sprint(batchSizes)), "[1000 1]"; got != want {
		t.Fatalf("batch sizes = %s, want %s", got, want)
	}
}

func newHTTPTestClient(t *testing.T, server *httptest.Server) *RealClient {
	t.Helper()
	transport := rewriteHostTransport{base: http.DefaultTransport, server: server}
	httpClient := &http.Client{Transport: transport}
	service, err := gmail.NewService(context.Background(),
		option.WithHTTPClient(httpClient),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new test Gmail service: %v", err)
	}
	return &RealClient{service: service}
}

type rewriteHostTransport struct {
	base   http.RoundTripper
	server *httptest.Server
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	requestURL := *req.URL
	parsed, err := url.Parse(t.server.URL)
	if err != nil {
		return nil, err
	}
	requestURL.Scheme = parsed.Scheme
	requestURL.Host = parsed.Host
	copyReq.URL = &requestURL
	copyReq.Host = requestURL.Host
	return t.base.RoundTrip(copyReq)
}
