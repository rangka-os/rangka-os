package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Attachment is one file attached to a record. The bytes live in the blob store under (org, SHA256).
type Attachment struct {
	ID          string    `json:"id"`
	Org         string    `json:"org,omitempty"`
	Model       string    `json:"model"`
	Record      string    `json:"record"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

const attCols = "id, org_id, model, record, filename, content_type, size, sha256, created_by, created_at"

func scanAttachment(r []any) Attachment {
	t, _ := r[9].(time.Time)
	return Attachment{ID: str(r[0]), Org: str(r[1]), Model: str(r[2]), Record: str(r[3]), Filename: str(r[4]),
		ContentType: str(r[5]), Size: toInt(r[6]), SHA256: str(r[7]), CreatedBy: str(r[8]), CreatedAt: t}
}

func attachments(rows [][]any) []Attachment {
	out := make([]Attachment, 0, len(rows))
	for _, r := range rows {
		out = append(out, scanAttachment(r))
	}
	return out
}

// InsertAttachment stores a row and fills its ID ("att_" + 20 hex) and CreatedAt.
func InsertAttachment(ctx context.Context, q pg.Querier, a *Attachment) error {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	a.ID = "att_" + hex.EncodeToString(b)
	res, err := q.Query(ctx, `insert into rk_attachments (id, org_id, model, record, filename, content_type, size, sha256, created_by)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9) returning created_at`,
		a.ID, a.Org, a.Model, a.Record, a.Filename, a.ContentType, a.Size, a.SHA256, a.CreatedBy)
	if err != nil {
		return err
	}
	a.CreatedAt, _ = res.Rows[0][0].(time.Time)
	return nil
}

// GetAttachment returns one attachment of the organisation, or ErrNotFound.
func GetAttachment(ctx context.Context, q pg.Querier, org, id string) (*Attachment, error) {
	res, err := q.Query(ctx, "select "+attCols+" from rk_attachments where org_id = $1 and id = $2", org, id)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrNotFound
	}
	a := scanAttachment(res.Rows[0])
	return &a, nil
}

// ListAttachments returns a record's attachments, newest first.
func ListAttachments(ctx context.Context, q pg.Querier, org, model, record string) ([]Attachment, error) {
	res, err := q.Query(ctx, "select "+attCols+" from rk_attachments where org_id = $1 and model = $2 and record = $3 order by created_at desc, id desc",
		org, model, record)
	if err != nil {
		return nil, err
	}
	return attachments(res.Rows), nil
}

// DeleteAttachment removes one row (the blob is the caller's to clean up, see CountBlobRefs).
func DeleteAttachment(ctx context.Context, q pg.Querier, org, id string) error {
	_, err := q.Exec(ctx, "delete from rk_attachments where org_id = $1 and id = $2", org, id)
	return err
}

// DeleteRecordAttachments removes all rows of a record and returns them.
func DeleteRecordAttachments(ctx context.Context, q pg.Querier, org, model, record string) ([]Attachment, error) {
	res, err := q.Query(ctx, "delete from rk_attachments where org_id = $1 and model = $2 and record = $3 returning "+attCols, org, model, record)
	if err != nil {
		return nil, err
	}
	return attachments(res.Rows), nil
}

// CountBlobRefs counts the rows of an organisation that point at a blob.
func CountBlobRefs(ctx context.Context, q pg.Querier, org, sum string) (int64, error) {
	res, err := q.Query(ctx, "select count(*) from rk_attachments where org_id = $1 and sha256 = $2", org, sum)
	if err != nil {
		return 0, err
	}
	return toInt(res.Rows[0][0]), nil
}

// AttachmentTotals returns the number of attachment rows and their summed size, over all organisations.
func AttachmentTotals(ctx context.Context, q pg.Querier) (count, bytes int64, err error) {
	res, err := q.Query(ctx, "select count(*), coalesce(sum(size), 0) from rk_attachments")
	if err != nil {
		return 0, 0, err
	}
	return toInt(res.Rows[0][0]), toInt(res.Rows[0][1]), nil
}

// RenameRecordAttachments moves a record's attachments to its new name (submit names a DRAFT-xxxxx record).
func RenameRecordAttachments(ctx context.Context, q pg.Querier, org, model, from, to string) error {
	_, err := q.Exec(ctx, "update rk_attachments set record = $4 where org_id = $1 and model = $2 and record = $3", org, model, from, to)
	return err
}
