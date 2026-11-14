package plan

import (
	"context"
	"fmt"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Storage and PDF limits (contract 0057 §1).

// DBUsedMB is the size of the tenant's database in whole megabytes, rounded up.
func DBUsedMB(ctx context.Context, q pg.Querier) (int64, error) {
	res, err := q.Query(ctx, "select ((pg_database_size(current_database()) + 1048575) / 1048576)::bigint")
	if err != nil || len(res.Rows) == 0 {
		return 0, err
	}
	return res.Rows[0][0].(int64), nil
}

// FilesUsedMB is the size of the stored attachments in whole megabytes, rounded up. A blob attached twice in one
// organisation is stored once and counted once.
func FilesUsedMB(ctx context.Context, q pg.Querier) (int64, error) {
	res, err := q.Query(ctx, `select ((coalesce(sum(size), 0) + 1048575) / 1048576)::bigint
		from (select distinct on (org_id, sha256) size from rk_attachments order by org_id, sha256) b`)
	if err != nil || len(res.Rows) == 0 {
		return 0, err
	}
	return res.Rows[0][0].(int64), nil
}

// CheckUpload refuses an attachment upload (files_mb or db_mb full) or an import (db_mb full), from the cached
// snapshot (it never takes a connection). Creating records is never refused for size, so a sale is never lost.
func (s *Snapshot) CheckUpload(reg *meta.Registry, files bool) *Error {
	if s == nil {
		return nil
	}
	if files {
		if lim, ok := s.Current.Limit(FilesMB); ok && s.Usage.FilesMB >= lim {
			return LimitError(reg, s.Current, FilesMB)
		}
	}
	if lim, ok := s.Current.Limit(DBMB); ok && s.Usage.DBMB >= lim {
		return LimitError(reg, s.Current, DBMB)
	}
	return nil
}

// PDFSlowPerHour is how many PDFs an hour a tenant past its monthly PDF limit still gets.
const PDFSlowPerHour = 10

// PDFUsed returns the PDFs rendered in period.
func PDFUsed(ctx context.Context, q pg.Querier, period string) (int64, error) {
	res, err := q.Query(ctx, "select used from rk_pdf_usage where period = $1", period)
	if err != nil || len(res.Rows) == 0 {
		return 0, err
	}
	return res.Rows[0][0].(int64), nil
}

// TakePDF counts one PDF render of the month now falls in. Past the plan's pdf_month limit renders are slowed, not
// refused: at most PDFSlowPerHour an hour; past that the error carries how long to wait (code pdf_slowed).
func TakePDF(ctx context.Context, q pg.Querier, reg *meta.Registry, now time.Time) error {
	c, err := Resolve(ctx, q, reg)
	if err != nil {
		return err
	}
	c.At = now
	period := Period(reg, now)
	res, err := q.Query(ctx, `insert into rk_pdf_usage (period, used) values ($1, 1)
		on conflict (period) do update set used = rk_pdf_usage.used + 1 returning used`, period)
	if err != nil {
		return err
	}
	used := res.Rows[0][0].(int64)
	lim, ok := c.Limit(PDFMonth)
	if !ok || used <= lim {
		return nil
	}
	loc := location(reg)
	local := now.In(loc)
	hour := local.Format("2006-01-02T15")
	res, err = q.Query(ctx, `update rk_pdf_usage set hour_used = case when hour = $2 then hour_used + 1 else 1 end, hour = $2
		where period = $1 returning hour_used`, period, hour)
	if err != nil {
		return err
	}
	if res.Rows[0][0].(int64) <= PDFSlowPerHour {
		return nil
	}
	if _, err := q.Query(ctx, "update rk_pdf_usage set used = used - 1, hour_used = hour_used - 1 where period = $1", period); err != nil {
		return err
	}
	next := local.Truncate(time.Hour).Add(time.Hour)
	wait := int(next.Sub(local).Seconds()) + 1
	return &Error{Code: "pdf_slowed", Resource: PDFMonth, RetryAfter: wait, Msg: fmt.Sprintf(
		"Jatah %d PDF bulan ini di paket %s sudah terpakai, jadi PDF dibuat pelan-pelan: %d per jam. PDF berikutnya bisa dibuat pukul %s. Versi HTML tetap bisa dicetak dari browser.%s",
		lim, c.Label(), PDFSlowPerHour, next.Format("15.04"), upgradeHint(reg, c, PDFMonth))}
}

func location(reg *meta.Registry) *time.Location {
	for _, k := range []string{"timezone", "scheduler.timezone"} {
		if tz, _ := reg.SiteConfig(k).(string); tz != "" {
			if l, err := time.LoadLocation(tz); err == nil {
				return l
			}
		}
	}
	return time.UTC
}
