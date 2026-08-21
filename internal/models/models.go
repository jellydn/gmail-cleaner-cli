// Package models holds gclean's universal data types. These types are the
// language spoken between storage, engine, gmailclient, and CLI packages.
// Field names mirror the Gmail API JSON shape so we can decode real and
// fixture responses with the same struct.
package models

import "time"

// Message is the universal representation of a Gmail message. We deliberately
// do not carry message bodies — body fetching violates the §15 local-only
// privacy default unless AI mode is engaged.
type Message struct {
	ID       string            `json:"id"`
	ThreadID string            `json:"threadId"`
	Sender   Sender            `json:"sender"`
	To       []string          `json:"to"`
	Subject  string            `json:"subject"`
	Date     time.Time         `json:"date"`
	Size     int64             `json:"size"` // estimated bytes
	Labels   []string          `json:"labels"`
	Headers  map[string]string `json:"headers"`
	Snippet  string            `json:"snippet"`
}

// Sender captures who sent the message plus whether they're in the user's
// Google Contacts (post People-API enrichment on scan).
type Sender struct {
	Email     string `json:"email"`
	Name      string `json:"name"`
	IsContact bool   `json:"isContact"`
}

// Classified is the output of running classifier+protector over a Message.
// ReasonCode is a stable enum-like string; see Reason* constants below.
type Classified struct {
	Message    *Message
	IsJunk     bool
	ReasonCode string
}

// Reason codes — stable strings used in stats and dry-run reports so users
// can grep their own data. Add new codes by appending; never reorder.
const (
	ReasonNewsletter  = "newsletter"
	ReasonMailingList = "mailing_list"
	ReasonNoreply     = "noreply"
	ReasonBulk        = "bulk"
	ReasonPromotion   = "promotion"
	ReasonSocial      = "social"
	ReasonCI          = "ci_notification"
	ReasonGitHub      = "github_notification"
	ReasonStripe      = "stripe_receipt"
	ReasonAWSBilling  = "aws_billing"
	ReasonAzureAlert  = "azure_alert"
	ReasonGitLab      = "gitlab_notification"
	ReasonJira        = "jira_notification"
	ReasonSlack       = "slack_notification"
	ReasonMarketing   = "marketing"

	ReasonContact     = "contact"
	ReasonReplied     = "replied"
	ReasonStarred     = "starred"
	ReasonImportant   = "important"
	ReasonSentByUser  = "sent_by_user"
	ReasonRecent      = "recent"
	ReasonWhitelisted = "whitelisted"
)

// Verdict is the final disposition from the planner.
type Verdict int

const (
	VerdictKeep Verdict = iota
	VerdictDelete
	VerdictArchive
	VerdictProtected // compute-only; never trashed
)

func (v Verdict) String() string {
	switch v {
	case VerdictKeep:
		return "keep"
	case VerdictDelete:
		return "delete"
	case VerdictArchive:
		return "archive"
	case VerdictProtected:
		return "protected"
	default:
		return "unknown"
	}
}

// Decision is the per-message planner output.
type Decision struct {
	Message    *Message
	Classified *Classified
	Verdict    Verdict
	Reasons    []string
}

// StatsReport mirrors the §5 example output. Sizes are bytes; the dashboard
// formatter converts to human-readable units.
type StatsReport struct {
	TotalMessages       int64
	EstimatedStorage    int64
	PotentialReclaim    int64
	LargestSender       SenderVolume
	NewsletterCount     int64
	NotificationCount   int64
	AttachmentsOver10MB int64
	ByCategory          map[string]int64
	ByYear              map[int]int64
}

// SenderVolume is one row of the sender ranking in StatsReport.
type SenderVolume struct {
	Email string
	Count int64
	Bytes int64
}

// SenderSafety aggregates one row per distinct sender. DeleteCount/DeleteBytes
// come from VerdictDelete after a dry-run pass. KeepCount is VerdictKeep +
// VerdictProtected (messages that won't be touched). Produced by
// storage.Store.Aggregations (a single messages-table scan) and consumed by
// the experimental Bubble Tea TUI. Living here (not in storage) keeps the
// report vocabulary in one home shared by the CLI, storage, and TUI.
type SenderSafety struct {
	Email       string
	TotalCount  int64
	TotalBytes  int64
	DeleteCount int64
	DeleteBytes int64
	KeepCount   int64
	Reasons     []string // distinct junk_reason values seen for this sender
}

// DryRunReport mirrors §5 — what cleanup WOULD do.
type DryRunReport struct {
	DeleteCount     int64
	RecoverBytes    int64
	KeepCount       int64
	ArchiveCount    int64
	DeleteBySender  map[string]int64
	RecoverByReason map[string]int64
	SampleDeletes   []string // first N message IDs+subjects for transparency
}
