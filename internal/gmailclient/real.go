package gmailclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gclean/internal/models"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// ErrCredentialsMissing is returned by NewRealClient when credentials.json
// is not present at the configured path.
var ErrCredentialsMissing = errors.New("gmail credentials.json not found; drop it into ~/.config/gclean/credentials.json or set GCLEAN_CREDENTIALS_PATH")

// RealClient talks to the real Gmail API.
type RealClient struct {
	credentialsPath string
	service         *gmail.Service
}

// NewRealClient validates that credentials.json exists, loads the persisted
// token, and builds an authenticated Gmail service. It returns
// ErrCredentialsMissing if the path is empty, and propagates I/O or auth
// errors otherwise.
func NewRealClient(credentialsPath string) (*RealClient, error) {
	if credentialsPath == "" {
		return nil, ErrCredentialsMissing
	}
	cfg, err := LoadConfig(credentialsPath)
	if err != nil {
		return nil, err
	}
	tok, err := LoadToken()
	if err != nil {
		return nil, fmt.Errorf("load token: %w (run `gclean login`)", err)
	}
	ctx := context.Background()
	ts := TokenSource(ctx, cfg, tok)
	svc, err := gmail.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("create gmail service: %w", err)
	}
	return &RealClient{
		credentialsPath: credentialsPath,
		service:         svc,
	}, nil
}

const (
	mutationBatchSize   = 1000
	maxMutationAttempts = 3

	// backoffBase is the first exponential-backoff wait. Google's error-handling
	// guidance recommends starting at 1s and doubling per retry, with up to 1s
	// of jitter (see jitter below). A 100ms-scale wait burns all attempts inside
	// the same per-minute quota window and is guaranteed to fail on real 429s.
	backoffBase = time.Second
	// backoffCap bounds the exponential backoff (Google recommends 32s max).
	backoffCap = 32 * time.Second
	// maxRetryAfterWait caps how long a server-provided Retry-After hint is
	// honored. Gmail can suggest very long waits on quota exhaustion; a CLI
	// should fail cleanly with a "try later" error instead of sleeping for
	// the full duration.
	maxRetryAfterWait = 60 * time.Second
)

func (r *RealClient) ListMessages(query string, max int) ([]*models.Message, error) {
	var out []*models.Message
	pageToken := ""
	for {
		listCall := r.service.Users.Messages.List("me").MaxResults(500).PageToken(pageToken)
		if query != "" {
			listCall.Q(query)
		}
		resp, err := listCall.Do()
		if err != nil {
			return nil, fmt.Errorf("list messages: %w", err)
		}
		slog.Info("listed page", "listed", len(resp.Messages), "fetched_so_far", len(out))
		for _, m := range resp.Messages {
			if max > 0 && len(out) >= max {
				return out, nil
			}
			full, err := r.service.Users.Messages.Get("me", m.Id).Format("metadata").MetadataHeaders(
				"From", "To", "Cc", "Subject", "Date",
				"List-Unsubscribe", "List-ID", "Precedence", "Auto-Submitted",
			).Do()
			if err != nil {
				return nil, fmt.Errorf("get message %s: %w", m.Id, err)
			}
			msg := mapGmailMessage(full)
			out = append(out, msg)
			if len(out)%100 == 0 {
				slog.Info("fetched metadata", "fetched_so_far", len(out))
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	slog.Info("list complete", "total", len(out))
	return out, nil
}

func (r *RealClient) TrashMessages(ids []string) error {
	for i, id := range ids {
		if err := r.retryMutation("trash message "+id, func() error {
			_, err := r.service.Users.Messages.Trash("me", id).Do()
			return err
		}); err != nil {
			return fmt.Errorf("trash message %d/%d (%s): %w", i+1, len(ids), id, err)
		}
	}
	return nil
}

func (r *RealClient) EmptyTrash() error {
	var (
		ids       []string
		pageToken string
	)
	for {
		call := r.service.Users.Messages.List("me").LabelIds("TRASH").MaxResults(mutationBatchSize)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		var resp *gmail.ListMessagesResponse
		if err := r.retryMutation("list trash", func() error {
			var err error
			resp, err = call.Do()
			return err
		}); err != nil {
			return err
		}
		for _, message := range resp.Messages {
			ids = append(ids, message.Id)
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	for start := 0; start < len(ids); start += mutationBatchSize {
		end := min(start+mutationBatchSize, len(ids))
		batch := &gmail.BatchDeleteMessagesRequest{Ids: ids[start:end]}
		err := r.retryMutation(fmt.Sprintf("empty trash batch %d-%d", start+1, end), func() error {
			return r.service.Users.Messages.BatchDelete("me", batch).Do()
		})
		if err != nil {
			if isScopeInsufficient(err) {
				// Google's backend requires the full mail.google.com scope for
				// batchDelete even though the docs say gmail.modify suffices
				// (googleapis/google-api-python-client#2710). Fall back to
				// per-message delete, which works with gmail.modify.
				slog.Warn("batchDelete not permitted by token scope; falling back to per-message delete", "count", len(batch.Ids))
				if err := r.deleteIndividually(batch.Ids); err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}
	return nil
}

// deleteIndividually permanently deletes messages one at a time. Used as the
// EmptyTrash fallback when the token lacks the full-access scope batchDelete
// requires; delete costs 10 quota units per message, so this is slower than
// one batchDelete call.
func (r *RealClient) deleteIndividually(ids []string) error {
	for i, id := range ids {
		if err := r.retryMutation("delete message "+id, func() error {
			return r.service.Users.Messages.Delete("me", id).Do()
		}); err != nil {
			return fmt.Errorf("delete message %d/%d (%s): %w", i+1, len(ids), id, err)
		}
	}
	return nil
}

// isScopeInsufficient reports whether a googleapi error is the 403 the Gmail
// backend returns when the token's scopes don't cover the endpoint.
func isScopeInsufficient(err error) bool {
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusForbidden {
		return false
	}
	// Google sometimes returns a plain-text body, so match against both the
	// parsed message and the raw body.
	haystack := apiErr.Message + "\n" + apiErr.Body
	return strings.Contains(haystack, "insufficient authentication scopes") ||
		strings.Contains(haystack, "ACCESS_TOKEN_SCOPE_INSUFFICIENT") ||
		strings.Contains(haystack, "insufficientPermissions")
}

func (r *RealClient) RestoreFromTrash(ids []string) error {
	for i, id := range ids {
		if err := r.retryMutation("restore message "+id, func() error {
			_, err := r.service.Users.Messages.Untrash("me", id).Do()
			return err
		}); err != nil {
			return fmt.Errorf("restore message %d/%d (%s): %w", i+1, len(ids), id, err)
		}
	}
	return nil
}

// InTrash returns the subset of ids whose TRASH label is present. It is used
// only on the reconcile path after a partial mutation, so per-ID gets are
// acceptable despite the read cost; each get carries the message's labelIds.
func (r *RealClient) InTrash(ids []string) ([]string, error) {
	in := []string{}
	for i, id := range ids {
		var msg *gmail.Message
		if err := r.retryMutation("get message "+id, func() error {
			var err error
			msg, err = r.service.Users.Messages.Get("me", id).Format("metadata").Do()
			return err
		}); err != nil {
			return nil, fmt.Errorf("get message %d/%d (%s): %w", i+1, len(ids), id, err)
		}
		for _, l := range msg.LabelIds {
			if l == "TRASH" {
				in = append(in, id)
				break
			}
		}
	}
	return in, nil
}

func (r *RealClient) retryMutation(operation string, fn func() error) error {
	for attempt := 1; attempt <= maxMutationAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if attempt == maxMutationAttempts || !isRetryableGmailError(err) {
			return fmt.Errorf("%s failed after %d attempt(s): %w", operation, attempt, err)
		}
		time.Sleep(retryDelay(attempt, err))
	}
	return fmt.Errorf("%s failed", operation)
}

// retryDelay computes the wait before retrying the attempt that just failed.
// It is a package variable (not a function) so tests can substitute a fast,
// deterministic stub — same pattern as oauthListenAddr.
var retryDelay = defaultRetryDelay

// defaultRetryDelay prefers the server's Retry-After hint (when the error
// carries one) and otherwise applies Google's recommended jittered
// exponential backoff: 1s doubling, capped at 32s.
func defaultRetryDelay(attempt int, err error) time.Duration {
	if d, ok := retryAfterDelay(err); ok {
		return d
	}
	exp := backoffBase << (attempt - 1)
	if exp > backoffCap {
		exp = backoffCap
	}
	return exp + jitter()
}

// jitter returns a random duration in [0, backoffBase), per Google's advice
// to add up to 1s of randomness to each backoff wait so concurrent clients
// don't retry in lockstep.
func jitter() time.Duration {
	return time.Duration(rand.Int64N(int64(backoffBase)))
}

// retryAfterDelay reads the Retry-After header from a googleapi error, when
// present. RFC 7231 allows delta-seconds (what Gmail sends) or an HTTP-date;
// both are parsed. The wait is capped at maxRetryAfterWait.
func retryAfterDelay(err error) (time.Duration, bool) {
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		return 0, false
	}
	header := strings.TrimSpace(apiErr.Header.Get("Retry-After"))
	if header == "" {
		return 0, false
	}
	var d time.Duration
	if secs, convErr := strconv.Atoi(header); convErr == nil {
		d = time.Duration(secs) * time.Second
	} else if t, parseErr := http.ParseTime(header); parseErr == nil {
		d = time.Until(t)
		if d < 0 {
			d = 0
		}
	} else {
		return 0, false
	}
	return min(d, maxRetryAfterWait), true
}

func isRetryableGmailError(err error) bool {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == 429 || apiErr.Code >= 500
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

func appendRecipients(to []string, header string) []string {
	if header == "" {
		return to
	}
	for _, recipient := range strings.Split(header, ",") {
		recipient = strings.TrimSpace(recipient)
		if recipient != "" {
			to = append(to, recipient)
		}
	}
	return to
}

func mapGmailMessage(m *gmail.Message) *models.Message {
	headers := make(map[string]string)
	for _, h := range m.Payload.Headers {
		headers[h.Name] = h.Value
	}
	var sender models.Sender
	if from, ok := headers["From"]; ok {
		addr, err := mail.ParseAddress(from)
		if err == nil {
			sender.Email = addr.Address
			if addr.Name != "" {
				sender.Name = addr.Name
			}
		} else {
			sender.Email = from
		}
	}
	to := appendRecipients(nil, headers["To"])
	to = appendRecipients(to, headers["Cc"])
	var date time.Time
	if m.InternalDate > 0 {
		date = time.UnixMilli(m.InternalDate)
	}
	return &models.Message{
		ID:       m.Id,
		ThreadID: m.ThreadId,
		Sender:   sender,
		To:       to,
		Subject:  headers["Subject"],
		Date:     date,
		Size:     int64(m.SizeEstimate),
		Labels:   m.LabelIds,
		Headers:  headers,
		Snippet:  m.Snippet,
	}
}
