package vector

import (
	"context"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/internal/indexversion"
)

// indexes 是 vector 的索引版本表，版本的规则见 indexversion。每个版本是一个 collection <prefix><name>_v<版本>。
var indexes = indexversion.Table{Name: "vector_indexes", Keys: "vector_keys", Superseded: errSuperseded}

// dropStale 删除已经废弃的版本的 collection，见 indexversion 的说明。
func (s *Store) dropStale(ctx context.Context, index string) error {
	st, err := indexes.Read(ctx, s.db, index, false)
	if err != nil {
		return err
	}
	below := max(st.Version, int(st.Building.Int64))
	names, err := s.client.ListCollections(ctx)
	if err != nil {
		return errors.Wrap(err, "vector: list collections")
	}
	prefix := s.prefix + index + "_v"
	for _, name := range names {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		v, err := strconv.Atoi(rest)
		if err != nil || strconv.Itoa(v) != rest || v >= below || v == st.Version {
			continue
		}
		if err := s.client.DeleteCollection(ctx, name); err != nil {
			return errors.Wrapf(err, "vector: delete collection %s", name)
		}
		s.mu.Lock()
		delete(s.ready, name)
		s.mu.Unlock()
	}
	return nil
}
