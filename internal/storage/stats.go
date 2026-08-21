package storage

import (
	"sort"
	"strconv"

	"gclean/internal/models"
)

// Aggregations is the single source of truth for every messages-table rollup.
// One full scan of the table produces the §5 StatsReport, the per-sender volume
// ranking, and the per-sender safety split. Previously Aggregate,
// SendersByVolume, and SenderSafety each scanned the table (two of them with
// near-identical GROUP BY sender_email clauses); consolidating them here means
// a schema change propagates to exactly one place and the ORDER BY magic-number
// foot-guns are gone. CONCERNS.md #2-adjacent locality win.
type Aggregations struct {
	Report      models.StatsReport
	BySender    []models.SenderVolume // every sender, sorted by bytes desc
	SendersSafe []models.SenderSafety // sorted by DeleteBytes desc, capped at 200
}

// Aggregations scans the messages table once and fills an Aggregations value.
func (s *Store) Aggregations() (*Aggregations, error) {
	rows, err := s.db.Query(`SELECT sender_email, subject, date, size, labels, junk_reason, is_junk, verdict FROM messages`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var (
		rep        models.StatsReport
		bySender   = map[string]*models.SenderVolume{}
		bySafety   = map[string]*models.SenderSafety{}
		byCategory = map[string]int64{}
		topSender  string
		topCount   int64
		reclaim    int64
	)
	rep.ByCategory = map[string]int64{}
	rep.ByYear = map[int]int64{}

	for rows.Next() {
		var (
			sender, subject, dateStr, labels, junkReason string
			size                                         int64
			isJunk, verdict                              int
		)
		if err := rows.Scan(&sender, &subject, &dateStr, &size, &labels, &junkReason, &isJunk, &verdict); err != nil {
			return nil, err
		}
		rep.TotalMessages++
		rep.EstimatedStorage += size

		switch junkReason {
		case models.ReasonNewsletter, models.ReasonMailingList:
			rep.NewsletterCount++
		}
		switch junkReason {
		case models.ReasonGitHub, models.ReasonGitLab,
			models.ReasonJira, models.ReasonSlack, models.ReasonNoreply, models.ReasonBulk,
			models.ReasonAzureAlert:
			rep.NotificationCount++
		}
		if size > 10*1024*1024 {
			rep.AttachmentsOver10MB++
		}
		for _, l := range splitCSV(labels) {
			byCategory[l]++
		}
		if len(dateStr) >= 4 {
			if y, err := strconv.Atoi(dateStr[:4]); err == nil {
				rep.ByYear[y]++
			}
		}

		sv := bySender[sender]
		if sv == nil {
			sv = &models.SenderVolume{Email: sender}
			bySender[sender] = sv
		}
		sv.Count++
		sv.Bytes += size

		ss := bySafety[sender]
		if ss == nil {
			ss = &models.SenderSafety{Email: sender}
			bySafety[sender] = ss
		}
		ss.TotalCount++
		ss.TotalBytes += size
		if verdict == int(models.VerdictDelete) {
			ss.DeleteCount++
			ss.DeleteBytes += size
			reclaim += size
		}
		if verdict == int(models.VerdictKeep) || verdict == int(models.VerdictProtected) {
			ss.KeepCount++
		}
		if junkReason != "" {
			addReason(&ss.Reasons, junkReason)
		}

		if sv.Count > topCount {
			topCount = sv.Count
			topSender = sender
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := &Aggregations{Report: rep}
	if topSender != "" {
		out.Report.LargestSender = models.SenderVolume{Email: topSender, Count: topCount, Bytes: bySender[topSender].Bytes}
	}
	out.Report.PotentialReclaim = reclaim
	out.Report.ByCategory = bucketByCategory(byCategory)

	out.BySender = make([]models.SenderVolume, 0, len(bySender))
	for _, sv := range bySender {
		out.BySender = append(out.BySender, *sv)
	}
	sortByBytesDesc(out.BySender)

	out.SendersSafe = make([]models.SenderSafety, 0, len(bySafety))
	for _, ss := range bySafety {
		out.SendersSafe = append(out.SendersSafe, *ss)
	}
	sortByDeleteBytesDesc(out.SendersSafe)
	if len(out.SendersSafe) > 200 {
		out.SendersSafe = out.SendersSafe[:200]
	}
	return out, nil
}

func sortByBytesDesc(s []models.SenderVolume) {
	sort.Slice(s, func(i, j int) bool { return s[i].Bytes > s[j].Bytes })
}

func sortByDeleteBytesDesc(s []models.SenderSafety) {
	sort.Slice(s, func(i, j int) bool { return s[i].DeleteBytes > s[j].DeleteBytes })
}

func addReason(rs *[]string, r string) {
	for _, e := range *rs {
		if e == r {
			return
		}
	}
	*rs = append(*rs, r)
}

// LargestAttachments returns the top N candidates with size above threshold.
func (s *Store) LargestAttachments(minBytes int64, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`SELECT id, thread_id, sender_email, sender_name, is_contact, subject, date, size, labels, headers, junk_reason, is_junk, protected, verdict, verdict_reasons
		FROM messages WHERE size >= ? ORDER BY size DESC LIMIT ?`, minBytes, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []StoredMessage{}
	for rows.Next() {
		var (
			m                            StoredMessage
			isContact, isJunk, protected int
		)
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.SenderEmail, &m.SenderName, &isContact, &m.Subject, &m.Date, &m.Size,
			&m.Labels, &m.Headers, &m.JunkReason, &isJunk, &protected, &m.Verdict, &m.VerdictReasons); err != nil {
			return nil, err
		}
		m.IsContact, m.IsJunk, m.Protected = isContact == 1, isJunk == 1, protected == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func bucketByCategory(counts map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for k, v := range counts {
		switch k {
		case "CATEGORY_PROMOTIONS":
			out["promotions"] += v
		case "CATEGORY_SOCIAL":
			out["social"] += v
		case "CATEGORY_UPDATES":
			out["updates"] += v
		case "CATEGORY_FORUMS":
			out["forums"] += v
		case "CATEGORY_PERSONAL":
			out["personal"] += v
		default:
			out["other"] += v
		}
	}
	return out
}
