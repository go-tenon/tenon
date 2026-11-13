package attachment

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/vector"
)

// ChunkDoc 是一个分段在向量索引中的文档，只放回答问题需要的字段。
type ChunkDoc struct {
	ArticleID uuid.UUID `json:"article_id" vector:"filter"`
	FileID    uuid.UUID `json:"file_id" vector:"filter"`
	Text      string    `json:"text"`
	Pages     []int     `json:"pages"`
	Context   string    `json:"-" vector:"embed"` // 带标题路径的正文，只用来计算向量
}

// Chunks 是附件分段的向量索引，key 是附件 ID。
var Chunks = vector.Define[ChunkDoc]("attachment-chunks")

// LoadChunks 读取附件当前的分段，附件删除后返回空，从索引中删除。
func LoadChunks(ctx context.Context, tx bun.IDB, key string) ([]ChunkDoc, error) {
	fileID, err := uuid.Parse(key)
	if err != nil {
		return nil, queue.Permanent(err)
	}
	var rows []AttachmentChunk
	err = tx.NewSelect().Model(&rows).Where("file_id = ?", fileID).Order("position").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load chunks")
	}
	docs := make([]ChunkDoc, len(rows))
	for i, r := range rows {
		docs[i] = ChunkDoc{ArticleID: r.ArticleID, FileID: r.FileID, Text: r.Text, Pages: r.Pages, Context: r.Context}
	}
	return docs, nil
}

// ScanChunkKeys 按 ID 的顺序列出 after 之后有分段的附件，重建索引时使用。
func ScanChunkKeys(ctx context.Context, db bun.IDB, after string, limit int) ([]string, error) {
	q := db.NewSelect().Model((*AttachmentChunk)(nil)).ColumnExpr("DISTINCT file_id").
		OrderExpr("file_id").Limit(int64(limit))
	if after != "" {
		id, err := uuid.Parse(after)
		if err != nil {
			return nil, queue.Permanent(err)
		}
		q = q.Where("file_id > ?", id)
	}
	var ids []uuid.UUID
	if err := q.Scan(ctx, &ids); err != nil {
		return nil, errors.Wrap(err, "scan chunk keys")
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = id.String()
	}
	return keys, nil
}
