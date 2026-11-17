// Package indexversion 管理 search 和 vector 共用的索引版本表。
//
// 索引的版本记在版本表中。查询只读 version；重建时登记一个更大的 building，同步同时写入两个版本，
// 重建任务把每个 key 写入 building，完成后 building 成为 version，旧版本的数据随后删除。
// 版本号只增不减：新的重建总是取比 version 和 building 都大的号，所以小于两者中较大者、又不是 version 的版本
// 一定已经废弃（被取代的重建、切换之前的版本），可以随时删除。没有记录的索引版本为 1。
package indexversion

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
)

// Table 是一张索引版本表。
type Table struct {
	Name       string           // 版本表，例如 search_indexes
	Keys       string           // 记录各索引有文档的 key 的表，例如 search_keys
	Superseded *errorx.Template // 重建被之后发起的重建取代时返回的错误
}

// State 是版本表中的一行。
type State struct {
	Version   int           `bun:"version"`      // 查询使用的版本
	Building  sql.NullInt64 `bun:"building"`     // 正在重建的版本
	Operation uuid.NullUUID `bun:"operation_id"` // 执行重建的操作
	LastKey   *string       `bun:"last_key"`     // 重建处理到的最后一个 key
}

// Versions 返回同步要写入的版本：查询使用的版本，以及正在重建的版本（如果有）。
//
// 它在锁住 key 之后读取，不加锁：一个同步如果在重建登记之前读到没有 building，重建任务处理这个 key 时
// 要等它提交，之后重新读取数据，它的修改因此包含在内；登记之后开始的同步一定读到 building。
func (t Table) Versions(ctx context.Context, db bun.IDB, index string) ([]int, error) {
	st, err := t.Read(ctx, db, index, false)
	if err != nil {
		return nil, err
	}
	if st.Building.Valid {
		return []int{st.Version, int(st.Building.Int64)}, nil
	}
	return []int{st.Version}, nil
}

// Read 读取索引的状态，没有记录时返回版本 1。lock 为 true 时锁住这一行直到事务结束。
func (t Table) Read(ctx context.Context, db bun.IDB, index string, lock bool) (State, error) {
	q := `SELECT version, building, operation_id, last_key FROM ` + t.Name + ` WHERE name = ?`
	if lock {
		q += ` FOR UPDATE`
	}
	var rows []State
	if err := db.NewRaw(q, index).Scan(ctx, &rows); err != nil {
		return State{}, errors.Wrapf(err, "%s: read index state", t.Name)
	}
	if len(rows) == 0 {
		return State{Version: 1}, nil
	}
	return rows[0], nil
}

// StartBuild 登记操作 op 重建的新版本，取代正在进行的重建，返回新版本号。
func (t Table) StartBuild(ctx context.Context, tx bun.IDB, index string, op uuid.UUID) (int, error) {
	// 先确保有这一行并锁住它：并发登记的重建依次取得版本号
	if _, err := tx.NewRaw(`INSERT INTO `+t.Name+` (name, version) VALUES (?, 1) `+
		`ON CONFLICT (name) DO UPDATE SET version = `+t.Name+`.version`, index).Exec(ctx); err != nil {
		return 0, errors.Wrapf(err, "%s: start reindex", t.Name)
	}
	st, err := t.Read(ctx, tx, index, true)
	if err != nil {
		return 0, err
	}
	building := max(st.Version, int(st.Building.Int64)) + 1
	_, err = tx.NewRaw(`UPDATE `+t.Name+` SET building = ?, operation_id = ?, last_key = NULL WHERE name = ?`,
		building, op, index).Exec(ctx)
	return building, errors.Wrapf(err, "%s: start reindex", t.Name)
}

// CurrentBuild 返回操作 op 重建的版本和处理到的 key。op 已被后发起的重建取代时返回 Superseded。
func (t Table) CurrentBuild(ctx context.Context, db bun.IDB, index string, op uuid.UUID, lock bool) (State, error) {
	st, err := t.Read(ctx, db, index, lock)
	if err != nil {
		return State{}, err
	}
	if !st.Building.Valid || st.Operation.UUID != op {
		return State{}, t.Superseded.New()
	}
	return st, nil
}

// SaveProgress 记下操作 op 的重建处理到的 key，与操作的进度在同一个事务中提交。
func (t Table) SaveProgress(ctx context.Context, tx bun.IDB, index string, op uuid.UUID, lastKey string) error {
	if _, err := t.CurrentBuild(ctx, tx, index, op, true); err != nil {
		return err
	}
	_, err := tx.NewRaw(`UPDATE `+t.Name+` SET last_key = ? WHERE name = ?`, lastKey, index).Exec(ctx)
	return errors.Wrapf(err, "%s: save reindex progress", t.Name)
}

// FinishBuild 把操作 op 重建的版本切换为查询使用的版本。
func (t Table) FinishBuild(ctx context.Context, tx bun.IDB, index string, op uuid.UUID) error {
	if _, err := t.CurrentBuild(ctx, tx, index, op, true); err != nil {
		return err
	}
	_, err := tx.NewRaw(`UPDATE `+t.Name+` SET version = building, building = NULL, operation_id = NULL, last_key = NULL `+
		`WHERE name = ?`, index).Exec(ctx)
	return errors.Wrapf(err, "%s: switch version", t.Name)
}

// CountKeys 返回索引中有文档的 key 的个数，作为重建的预计单位数。
func (t Table) CountKeys(ctx context.Context, db bun.IDB, index string) (int, error) {
	var n int
	err := db.NewRaw(`SELECT COUNT(*) FROM `+t.Keys+` WHERE index_name = ?`, index).Scan(ctx, &n)
	return n, errors.Wrapf(err, "%s: count keys", t.Keys)
}
